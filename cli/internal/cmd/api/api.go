// Package api è `gs api <percorso>`: una chiamata grezza all'API di GitStack,
// in stile `gh api`, per quello che i comandi dedicati non coprono
// (amministrazione: organizzazioni, team, webhook, utenti, token agent; G4).
//
// Il percorso è relativo a /api/v1 e si risolve sull'istanza corrente (G7):
// `gs api /user/tokens` e `gs api user/tokens` sono la stessa chiamata. Si
// può scrivere anche con il prefisso (`/api/v1/orgs`). Un indirizzo
// completo non è ammesso: l'istanza è quella scelta con --hostname, GS_HOST o
// il remote.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// maxPages ferma una paginazione che non finisce mai.
const maxPages = 1000

type options struct {
	method   string
	raw      []string // -f
	typed    []string // -F
	headers  []string // -H
	input    string
	paginate bool
	include  bool
	jq       string
}

// NewCmd restituisce `gs api`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:   "api <percorso>",
		Short: "Chiama l'API di GitStack",
		Long: "Fa una richiesta autenticata all'API dell'istanza corrente e stampa la risposta. Il percorso è relativo a " +
			"/api/v1 (`/orgs`, `/repos/alice/web/issues`).\n\n" +
			"Il metodo è GET, o POST se ci sono campi (-f, -F) o un corpo (--input); si cambia con -X. Sui GET i campi " +
			"diventano parametri di query, sugli altri metodi un corpo JSON.\n\n" +
			"  -f chiave=valore   campo di testo\n" +
			"  -F chiave=valore   campo tipizzato: true, false, null e interi sono JSON; @file legge il valore da un file (@- da stdin)\n" +
			"  --input file       corpo da file (- = stdin), inviato così com'è; alternativo ai campi\n" +
			"  -H 'Nome: valore'  intestazione aggiuntiva\n" +
			"  --paginate         segue le pagine ({items,page,perPage,total}) e unisce gli elementi in un solo array\n" +
			"  --jq espressione   filtra la risposta (con --paginate: la risposta unita)\n\n" +
			"Chiavi annidate: `a[b]=1` dà {\"a\":{\"b\":1}}, `a[]=x` aggiunge a un array.\n\n" +
			"Un errore dell'API stampa il corpo della risposta su stdout, il messaggio su stderr (JSON con --jq) ed esce con " +
			"il codice di G3: 4 non autenticato, 5 permesso negato, 6 non trovato, 1 altro.",
		Example: "  gs api /orgs --paginate --jq '.[].name'\n" +
			"  gs api /orgs -f name=acme -f displayName=Acme\n" +
			"  gs api -X PATCH /repos/alice/web -F archived=true\n" +
			"  gs api /orgs/acme/webhooks --input webhook.json\n" +
			"  gs api /users/botty/tokens -f name=ci -F 'scopes[]=read:resource'",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmdutil.UsageErrorf("serve un percorso: gs api <percorso> (per esempio `gs api /orgs`)")
			}
			return run(c, f, &o, args[0])
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.method, "method", "X", "", "Metodo HTTP (default GET, POST con campi o corpo)")
	fl.StringArrayVarP(&o.raw, "raw-field", "f", nil, "Campo di testo `chiave=valore` (ripetibile)")
	fl.StringArrayVarP(&o.typed, "field", "F", nil, "Campo tipizzato `chiave=valore` (ripetibile; @file legge da file)")
	fl.StringArrayVarP(&o.headers, "header", "H", nil, "Intestazione `'Nome: valore'` (ripetibile)")
	fl.StringVar(&o.input, "input", "", "Corpo della richiesta da `file` (- = stdin)")
	fl.BoolVar(&o.paginate, "paginate", false, "Segue le pagine e unisce gli elementi")
	fl.BoolVarP(&o.include, "include", "i", false, "Stampa stato e intestazioni della risposta")
	fl.StringVarP(&o.jq, "jq", "q", "", "Filtra la risposta JSON con un'espressione jq")
	return cmd
}

func run(c *cobra.Command, f *cmdutil.Factory, o *options, rawPath string) error {
	path, err := normalizePath(rawPath)
	if err != nil {
		return err
	}
	fields, err := buildFields(f, o)
	if err != nil {
		return err
	}
	hasFields := len(o.raw)+len(o.typed) > 0
	if hasFields && o.input != "" {
		return cmdutil.UsageErrorf("--input non si combina con -f/-F")
	}
	method := strings.ToUpper(strings.TrimSpace(o.method))
	if method == "" {
		method = http.MethodGet
		if hasFields || o.input != "" {
			method = http.MethodPost
		}
	}
	if !regexp.MustCompile(`^[A-Z]+$`).MatchString(method) {
		return cmdutil.UsageErrorf("metodo non valido: %q", o.method)
	}
	if o.paginate && method != http.MethodGet {
		return cmdutil.UsageErrorf("--paginate vale solo per GET")
	}
	if o.paginate && o.include {
		return cmdutil.UsageErrorf("--paginate non si combina con --include")
	}
	hdr, err := parseHeaders(o.headers)
	if err != nil {
		return err
	}

	// Corpo o query.
	var body []byte
	query := url.Values{}
	if i := strings.Index(path, "?"); i >= 0 {
		q, err := url.ParseQuery(path[i+1:])
		if err != nil {
			return cmdutil.UsageErrorf("parametri di query non validi in %q: %v", rawPath, err)
		}
		query, path = q, path[:i]
	}
	switch {
	case o.input != "":
		if body, err = readInput(f, o.input); err != nil {
			return err
		}
	case hasFields && (method == http.MethodGet || method == http.MethodHead):
		flatten(query, "", fields)
	case hasFields:
		if body, err = json.Marshal(fields); err != nil {
			return err
		}
	}
	if o.jq != "" {
		// Valida l'espressione prima della rete: un uso errato esce 2 senza chiamare.
		if err := (&output.JSONOptions{JQ: o.jq}).Write(io.Discard, nil, false); err != nil {
			var ue *cmdutil.UsageError
			if errors.As(err, &ue) {
				return err
			}
		}
	}

	cl, err := api.FromFactory(f)
	if err != nil {
		return err
	}
	do := func(q url.Values) (*http.Response, []byte, error) {
		p := path
		if len(q) > 0 {
			p += "?" + q.Encode()
		}
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		resp, err := cl.Do(c.Context(), method, p, rd, hdr)
		if err != nil {
			return nil, nil, writeErrorBody(f, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return resp, b, err
	}

	if o.paginate {
		return paginate(f, o, query, do)
	}
	resp, b, err := do(query)
	if err != nil {
		return err
	}
	if o.include {
		_, _ = fmt.Fprintf(f.IO.Out, "%s %s\n", resp.Proto, resp.Status)
		keys := make([]string, 0, len(resp.Header))
		for k := range resp.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = fmt.Fprintf(f.IO.Out, "%s: %s\n", k, strings.Join(resp.Header[k], ", "))
		}
		_, _ = fmt.Fprintln(f.IO.Out)
	}
	return printBody(f, o, b)
}

// writeErrorBody stampa su stdout il corpo di una risposta di errore (come
// gh) e restituisce l'errore, che root mappa sul codice di uscita.
func writeErrorBody(f *cmdutil.Factory, err error) error {
	var ae *cmdutil.APIError
	if errors.As(err, &ae) && len(bytes.TrimSpace(ae.Body)) > 0 {
		_, _ = f.IO.Out.Write(ae.Body)
		if !bytes.HasSuffix(ae.Body, []byte("\n")) {
			_, _ = fmt.Fprintln(f.IO.Out)
		}
	}
	return err
}

// printBody stampa la risposta: con --jq filtrata, altrimenti com'è (JSON
// indentato su un terminale).
func printBody(f *cmdutil.Factory, o *options, b []byte) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if o.jq != "" {
		var v any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("--jq: la risposta non è JSON: %w", err)
		}
		return (&output.JSONOptions{JQ: o.jq}).Write(f.IO.Out, v, f.IO.OutTTY)
	}
	if f.IO.OutTTY && json.Valid(b) {
		var buf bytes.Buffer
		if json.Indent(&buf, b, "", "  ") == nil {
			buf.WriteByte('\n')
			_, err := f.IO.Out.Write(buf.Bytes())
			return err
		}
	}
	_, err := f.IO.Out.Write(b)
	if err == nil && !bytes.HasSuffix(b, []byte("\n")) {
		_, err = fmt.Fprintln(f.IO.Out)
	}
	return err
}

// paginate segue le pagine {items,page,perPage,total} e unisce gli elementi.
// Una risposta che non è una pagina (un array o un oggetto senza items) si
// stampa com'è, dopo la prima richiesta.
func paginate(f *cmdutil.Factory, o *options, query url.Values, do func(url.Values) (*http.Response, []byte, error)) error {
	var items []any
	per := 100
	if v := query.Get("perPage"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			per = n
		}
	}
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("perPage", strconv.Itoa(per))
		q.Set("page", strconv.Itoa(page))
		_, b, err := do(q)
		if err != nil {
			return err
		}
		var pg struct {
			Items   *[]any `json:"items"`
			PerPage int    `json:"perPage"`
			Total   int    `json:"total"`
		}
		if json.Unmarshal(b, &pg) != nil || pg.Items == nil {
			if page == 1 {
				return printBody(f, o, b)
			}
			return fmt.Errorf("risposta non paginata alla pagina %d", page)
		}
		items = append(items, *pg.Items...)
		eff := pg.PerPage
		if eff <= 0 {
			eff = per
		}
		if len(*pg.Items) == 0 || page*eff >= pg.Total {
			break
		}
	}
	if items == nil {
		items = []any{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return printBody(f, o, b)
}

// normalizePath rende il percorso relativo alla base /api/v1 e rifiuta gli
// indirizzi completi.
func normalizePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.Contains(p, "://") {
		return "", cmdutil.UsageErrorf("il percorso è relativo a /api/v1 sull'istanza corrente, non un indirizzo completo (%q): usa --hostname per cambiare istanza", p)
	}
	if p == "" || p == "/" {
		return "", cmdutil.UsageErrorf("percorso vuoto: gs api <percorso>")
	}
	p = "/" + strings.TrimLeft(p, "/")
	if p == api.BasePath || strings.HasPrefix(p, api.BasePath+"/") || strings.HasPrefix(p, api.BasePath+"?") {
		p = strings.TrimPrefix(p, api.BasePath)
		if p == "" || strings.HasPrefix(p, "?") {
			p = "/" + p
		}
	}
	return p, nil
}

func parseHeaders(hs []string) (http.Header, error) {
	h := http.Header{}
	for _, s := range hs {
		name, val, ok := strings.Cut(s, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, cmdutil.UsageErrorf("intestazione non valida %q: attesa `Nome: valore`", s)
		}
		h.Add(name, strings.TrimSpace(val))
	}
	return h, nil
}

func readInput(f *cmdutil.Factory, file string) ([]byte, error) {
	var (
		b   []byte
		err error
	)
	if file == "-" {
		b, err = io.ReadAll(f.IO.In)
	} else {
		b, err = os.ReadFile(file)
	}
	if err != nil {
		return nil, fmt.Errorf("lettura del corpo da %s: %w", file, err)
	}
	return b, nil
}

var intRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// buildFields costruisce l'oggetto dei campi -f e -F, nell'ordine dato.
func buildFields(f *cmdutil.Factory, o *options) (map[string]any, error) {
	m := map[string]any{}
	for _, s := range o.raw {
		k, v, err := splitField(s, "-f")
		if err != nil {
			return nil, err
		}
		if err := setField(m, k, v); err != nil {
			return nil, err
		}
	}
	for _, s := range o.typed {
		k, v, err := splitField(s, "-F")
		if err != nil {
			return nil, err
		}
		val, err := typedValue(f, v)
		if err != nil {
			return nil, err
		}
		if err := setField(m, k, val); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func splitField(s, flag string) (string, string, error) {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return "", "", cmdutil.UsageErrorf("campo non valido per %s: %q (atteso chiave=valore)", flag, s)
	}
	return k, v, nil
}

func typedValue(f *cmdutil.Factory, v string) (any, error) {
	switch {
	case v == "true":
		return true, nil
	case v == "false":
		return false, nil
	case v == "null":
		return nil, nil
	case intRe.MatchString(v):
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, nil
		}
	case strings.HasPrefix(v, "@"):
		b, err := readInput(f, strings.TrimPrefix(v, "@"))
		if err != nil {
			return nil, err
		}
		return string(b), nil
	}
	return v, nil
}

var keyPartRe = regexp.MustCompile(`\[([^\[\]]*)\]`)

// setField imposta m[chiave], con `a[b][c]` per gli oggetti annidati e `a[]`
// per gli array.
func setField(m map[string]any, key string, val any) error {
	base := key
	var parts []string
	if i := strings.Index(key, "["); i > 0 {
		base = key[:i]
		rest := key[i:]
		ms := keyPartRe.FindAllStringSubmatch(rest, -1)
		joined := ""
		for _, x := range ms {
			joined += x[0]
			parts = append(parts, x[1])
		}
		if joined != rest {
			return cmdutil.UsageErrorf("nome di campo non valido: %q", key)
		}
	}
	if base == "" {
		return cmdutil.UsageErrorf("nome di campo non valido: %q", key)
	}
	if len(parts) == 0 {
		m[base] = val
		return nil
	}
	cur := any(m[base])
	set := func(v any) { m[base] = v }
	return assign(cur, set, parts, val, key)
}

// assign scende lungo parts creando oggetti e array. set scrive il nodo nel suo genitore.
func assign(cur any, set func(any), parts []string, val any, key string) error {
	p := parts[0]
	if p == "" { // array
		arr, _ := cur.([]any)
		if cur != nil && arr == nil {
			return cmdutil.UsageErrorf("il campo %q è già usato con un altro tipo", key)
		}
		if len(parts) == 1 {
			set(append(arr, val))
			return nil
		}
		return cmdutil.UsageErrorf("`[]` vale solo in fondo alla chiave: %q", key)
	}
	obj, _ := cur.(map[string]any)
	if cur != nil && obj == nil {
		return cmdutil.UsageErrorf("il campo %q è già usato con un altro tipo", key)
	}
	if obj == nil {
		obj = map[string]any{}
		set(obj)
	}
	if len(parts) == 1 {
		obj[p] = val
		return nil
	}
	return assign(obj[p], func(v any) { obj[p] = v }, parts[1:], val, key)
}

// flatten porta i campi in parametri di query (GET): annidati come a[b], array ripetuti.
func flatten(q url.Values, prefix string, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			name := k
			if prefix != "" {
				name = prefix + "[" + k + "]"
			}
			flatten(q, name, x[k])
		}
	case []any:
		for _, e := range x {
			flatten(q, prefix, e)
		}
	case nil:
		q.Add(prefix, "")
	default:
		q.Add(prefix, fmt.Sprint(x))
	}
}
