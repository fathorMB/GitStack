package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// Caller è il client dell'API di GitStack (api.Client lo soddisfa): path è
// relativo a /api/v1, e una risposta non 2xx torna come *cmdutil.APIError.
type Caller interface {
	Do(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error)
}

// Options configura un Server.
type Options struct {
	Client   Caller
	Version  string // versione di gs
	ReadOnly bool   // --read-only (M3): solo letture, anche per api_request
	In       io.Reader
	Out      io.Writer
}

// Server è un server MCP su stdio (JSON-RPC 2.0, un messaggio per riga).
type Server struct {
	o     Options
	tools map[string]Tool
	mu    sync.Mutex // serializza le scritture su Out
}

// ApiRequestName è lo strumento generico: qualsiasi operazione dell'API, entro
// i permessi del token.
const ApiRequestName = "api_request"

// maxBody limita quanto di una risposta torna al modello.
const maxBody = 1 << 20

// protocolVersions sono le revisioni di MCP che il server sa parlare, dalla più recente.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// NewServer costruisce il server con gli strumenti generati dal contratto.
func NewServer(o Options) *Server {
	s := &Server{o: o, tools: map[string]Tool{}}
	for _, t := range Tools {
		s.tools[t.Name] = t
	}
	return s
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve legge richieste da In finché non finisce (EOF) o ctx si chiude.
func (s *Server) Serve(ctx context.Context) error {
	rd := bufio.NewReaderSize(s.o.In, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.handleLine(ctx, bytes.TrimSpace(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handleLine(ctx context.Context, line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.reply(response{ID: json.RawMessage("null"), Error: &rpcError{codeParse, "JSON non valido"}})
		return
	}
	notification := len(req.ID) == 0
	if req.Method == "" {
		if !notification {
			s.reply(response{ID: req.ID, Error: &rpcError{codeInvalidRequest, "manca method"}})
		}
		return
	}
	result, rerr := s.dispatch(ctx, req)
	if notification {
		return
	}
	if rerr != nil {
		s.reply(response{ID: req.ID, Error: rerr})
		return
	}
	s.reply(response{ID: req.ID, Result: result})
}

func (s *Server) reply(r response) {
	r.JSONRPC = "2.0"
	b, err := json.Marshal(r)
	if err != nil {
		b, _ = json.Marshal(response{JSONRPC: "2.0", ID: r.ID, Error: &rpcError{-32603, "risposta non serializzabile"}})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.o.Out.Write(append(b, '\n'))
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "gs", "version": s.o.Version},
			"instructions":    s.instructions(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.list()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{codeInvalidParams, "tools/call vuole name e arguments"}
		}
		res, ok := s.call(ctx, p.Name, p.Arguments)
		if !ok {
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf("strumento sconosciuto: %s", p.Name)}
		}
		return res, nil
	}
	return nil, &rpcError{codeMethodNotFound, "metodo non supportato: " + req.Method}
}

func (s *Server) instructions() string {
	txt := "Strumenti di GitStack (oggetto_azione) generati dal contratto dell'API; api_request chiama qualsiasi altra operazione " +
		"entro i permessi del token. Il server non chiede conferme: le azioni distruttive sono annotate."
	if s.o.ReadOnly {
		txt += " Modalità sola lettura: le scritture sono rifiutate."
	}
	return txt
}

type annotations struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type toolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations annotations     `json:"annotations"`
}

const apiRequestSchema = `{"type":"object","properties":{` +
	`"method":{"type":"string","enum":["GET","POST","PUT","PATCH","DELETE"],"description":"Metodo HTTP (default GET)."},` +
	`"path":{"type":"string","description":"Percorso relativo a /api/v1, per esempio /repos/alice/web/issues. Senza query: usa il campo query."},` +
	`"query":{"type":"object","description":"Parametri di query (valori: testo, numero, booleano o elenco)."},` +
	`"body":{"description":"Corpo JSON della richiesta."}},` +
	`"required":["path"],"additionalProperties":false}`

func (s *Server) list() []toolInfo {
	var out []toolInfo
	for _, t := range s.tools {
		if s.o.ReadOnly && !t.ReadOnly() {
			continue
		}
		out = append(out, toolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.Schema),
			Annotations: annotations{Title: t.Name, ReadOnlyHint: t.ReadOnly(), DestructiveHint: !t.ReadOnly() && t.Destructive},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	desc := "Chiama una qualsiasi operazione dell'API di GitStack (contratto OpenAPI) entro i permessi del token."
	if s.o.ReadOnly {
		desc = "Legge dall'API di GitStack (solo GET: il server è in modalità --read-only)."
	}
	out = append(out, toolInfo{
		Name:        ApiRequestName,
		Description: desc,
		InputSchema: json.RawMessage(apiRequestSchema),
		Annotations: annotations{Title: ApiRequestName, ReadOnlyHint: s.o.ReadOnly, DestructiveHint: !s.o.ReadOnly},
	})
	return out
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError"`
}

func text(msg string, isErr bool) callResult {
	return callResult{Content: []content{{Type: "text", Text: msg}}, IsError: isErr}
}

// call esegue uno strumento; ok=false se il nome non esiste.
func (s *Server) call(ctx context.Context, name string, rawArgs json.RawMessage) (callResult, bool) {
	args := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(rawArgs)) > 0 && string(bytes.TrimSpace(rawArgs)) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return text("arguments deve essere un oggetto JSON", true), true
		}
	}
	if name == ApiRequestName {
		return s.apiRequest(ctx, args), true
	}
	t, ok := s.tools[name]
	if !ok {
		return callResult{}, false
	}
	if s.o.ReadOnly && !t.ReadOnly() {
		return text(fmt.Sprintf("%s scrive: il server è in modalità --read-only, sono ammesse solo le letture", name), true), true
	}
	return s.callTool(ctx, t, args), true
}

func (s *Server) callTool(ctx context.Context, t Tool, args map[string]json.RawMessage) callResult {
	known := map[string]bool{}
	path := t.Path
	for _, p := range t.PathParams {
		known[p] = true
		raw, ok := args[p]
		if !ok {
			return text(fmt.Sprintf("manca il parametro %q", p), true)
		}
		v, err := scalar(raw)
		if err != nil || v == "" {
			return text(fmt.Sprintf("il parametro %q deve essere un valore semplice non vuoto", p), true)
		}
		path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape(v))
	}
	q := url.Values{}
	for _, p := range t.QueryParams {
		known[p] = true
		raw, ok := args[p]
		if !ok {
			continue
		}
		vals, err := scalars(raw)
		if err != nil {
			return text(fmt.Sprintf("il parametro %q deve essere testo, numero, booleano o un elenco di questi", p), true)
		}
		for _, v := range vals {
			q.Add(p, v)
		}
	}
	var body io.Reader
	if t.Body {
		known["body"] = true
		if raw, ok := args["body"]; ok && string(bytes.TrimSpace(raw)) != "null" {
			body = bytes.NewReader(raw)
		}
	}
	for k := range args {
		if !known[k] {
			return text(fmt.Sprintf("parametro sconosciuto %q per %s", k, t.Name), true)
		}
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return s.do(ctx, t.Method, path, body)
}

func (s *Server) apiRequest(ctx context.Context, args map[string]json.RawMessage) callResult {
	for k := range args {
		switch k {
		case "method", "path", "query", "body":
		default:
			return text(fmt.Sprintf("parametro sconosciuto %q per api_request", k), true)
		}
	}
	method := http.MethodGet
	if raw, ok := args["method"]; ok {
		if err := json.Unmarshal(raw, &method); err != nil {
			return text("method deve essere testo", true)
		}
		method = strings.ToUpper(strings.TrimSpace(method))
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return text(fmt.Sprintf("metodo non ammesso: %q", method), true)
	}
	if s.o.ReadOnly && method != http.MethodGet {
		return text(fmt.Sprintf("api_request %s rifiutato: il server è in modalità --read-only, è ammesso solo GET", method), true)
	}
	var rawPath string
	if err := json.Unmarshal(args["path"], &rawPath); err != nil || strings.TrimSpace(rawPath) == "" {
		return text("path è obbligatorio ed è testo", true)
	}
	path, err := normalizePath(rawPath)
	if err != nil {
		return text(err.Error(), true)
	}
	q := url.Values{}
	if raw, ok := args["query"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return text("query deve essere un oggetto", true)
		}
		for k, v := range m {
			vals, err := scalars(v)
			if err != nil {
				return text(fmt.Sprintf("query.%s deve essere testo, numero, booleano o un elenco di questi", k), true)
			}
			for _, x := range vals {
				q.Add(k, x)
			}
		}
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var body io.Reader
	if raw, ok := args["body"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if method == http.MethodGet {
			return text("un GET non ha corpo", true)
		}
		body = bytes.NewReader(raw)
	}
	return s.do(ctx, method, path, body)
}

// normalizePath rende relativo a /api/v1 il percorso di api_request: niente
// indirizzi completi, niente «..», niente query nel percorso.
func normalizePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.Contains(p, "://") || strings.HasPrefix(p, "//") {
		return "", errors.New("path è relativo a /api/v1: un indirizzo completo non è ammesso")
	}
	if strings.ContainsAny(p, "?#") {
		return "", errors.New("path non porta la query: usa il campo query")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/api/v1" || strings.HasPrefix(p, "/api/v1/") {
		p = strings.TrimPrefix(p, "/api/v1")
		if p == "" {
			p = "/"
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return "", errors.New("path non può contenere «.» o «..»")
		}
	}
	return p, nil
}

func (s *Server) do(ctx context.Context, method, path string, body io.Reader) callResult {
	resp, err := s.o.Client.Do(ctx, method, path, body, nil)
	if err != nil {
		var ae *cmdutil.APIError
		if errors.As(err, &ae) {
			msg := ae.Error()
			if len(ae.Body) > 0 {
				msg += "\n" + truncate(string(ae.Body))
			}
			return text(msg, true)
		}
		return text("richiesta fallita: "+err.Error(), true)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return text("lettura della risposta fallita: "+err.Error(), true)
	}
	if len(b) == 0 {
		return text(fmt.Sprintf("HTTP %d, nessun contenuto", resp.StatusCode), false)
	}
	return text(truncate(string(b)), false)
}

func truncate(s string) string {
	if len(s) > maxBody {
		return s[:maxBody] + "\n… risposta troncata a 1 MiB: restringi la richiesta (filtri, paginazione)"
	}
	return s
}

// scalar legge un valore JSON semplice come testo.
func scalar(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	switch {
	case string(raw) == "true" || string(raw) == "false":
		return string(raw), nil
	case len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9'):
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil {
			return n.String(), nil
		}
	}
	return "", errors.New("non è un valore semplice")
}

// scalars legge un valore semplice o un elenco di valori semplici; null è vuoto.
func scalars(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > 0 && raw[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		var out []string
		for _, e := range list {
			v, err := scalar(e)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	v, err := scalar(raw)
	if err != nil {
		return nil, err
	}
	return []string{v}, nil
}
