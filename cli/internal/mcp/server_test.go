package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

type call struct {
	method, path, body string
}

type fakeAPI struct {
	calls []call
	fail  int // se != 0, risponde con questo stato
}

func (f *fakeAPI) Do(_ context.Context, method, path string, body io.Reader, _ http.Header) (*http.Response, error) {
	c := call{method: method, path: path}
	if body != nil {
		b, _ := io.ReadAll(body)
		c.body = string(b)
	}
	f.calls = append(f.calls, c)
	if f.fail != 0 {
		return nil, &cmdutil.APIError{Status: f.fail, Code: "not_found", Message: "non trovato", Body: []byte(`{"error":{"code":"not_found"}}`)}
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
}

// client è un client MCP minimo: manda le richieste in fila e legge le risposte.
type client struct {
	t   *testing.T
	in  bytes.Buffer
	n   int
	api *fakeAPI
}

func (c *client) send(method string, params any) int {
	c.n++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.n, "method": method, "params": params})
	c.in.Write(append(b, '\n'))
	return c.n
}

type rpcResp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *client) run(readOnly bool) map[int]rpcResp {
	c.t.Helper()
	var out bytes.Buffer
	srv := NewServer(Options{Client: c.api, Version: "1.2.3", ReadOnly: readOnly, In: &c.in, Out: &out})
	if err := srv.Serve(context.Background()); err != nil {
		c.t.Fatal(err)
	}
	res := map[int]rpcResp{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r rpcResp
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			c.t.Fatalf("risposta non JSON: %q", line)
		}
		res[r.ID] = r
	}
	return res
}

type listed struct {
	Tools []struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Annotations annotations     `json:"annotations"`
	} `json:"tools"`
}

func newClient(t *testing.T) *client {
	return &client{t: t, api: &fakeAPI{}}
}

func decodeCall(t *testing.T, r rpcResp) callResult {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("errore JSON-RPC: %+v", r.Error)
	}
	var cr callResult
	if err := json.Unmarshal(r.Result, &cr); err != nil {
		t.Fatal(err)
	}
	return cr
}

func TestInitializeEElenco(t *testing.T) {
	c := newClient(t)
	i := c.send("initialize", map[string]any{"protocolVersion": "2025-03-26"})
	l := c.send("tools/list", nil)
	p := c.send("ping", nil)
	u := c.send("boh", nil)
	res := c.run(false)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct{ Name, Version string }
	}
	_ = json.Unmarshal(res[i].Result, &init)
	if init.ProtocolVersion != "2025-03-26" || init.ServerInfo.Name != "gs" || init.ServerInfo.Version != "1.2.3" {
		t.Errorf("initialize: %+v", init)
	}
	if string(res[p].Result) != "{}" {
		t.Errorf("ping: %s", res[p].Result)
	}
	if res[u].Error == nil || res[u].Error.Code != codeMethodNotFound {
		t.Errorf("metodo sconosciuto: %+v", res[u])
	}

	var ls listed
	_ = json.Unmarshal(res[l].Result, &ls)
	by := map[string]int{}
	for k, tl := range ls.Tools {
		by[tl.Name] = k
		if !json.Valid(tl.InputSchema) {
			t.Errorf("%s: schema non valido", tl.Name)
		}
	}
	if len(ls.Tools) != len(Tools)+1 {
		t.Errorf("strumenti: %d, attesi %d", len(ls.Tools), len(Tools)+1)
	}
	for _, name := range []string{"pr_list", "pr_merge", "issue_create", "code_file", "label_list", "milestone_list", "issue_search", "notification_list", "commit_status_create", ApiRequestName} {
		if _, ok := by[name]; !ok {
			t.Errorf("strumento %s assente", name)
		}
	}
	// Annotazioni (M3): distruttive su chiusure, merge, eliminazioni e occultamenti; mai sulle letture.
	for _, name := range []string{"pr_merge", "pr_close", "issue_close", "issue_hide", "label_delete", "milestone_delete", "pr_comment_delete"} {
		a := ls.Tools[by[name]].Annotations
		if !a.DestructiveHint || a.ReadOnlyHint {
			t.Errorf("%s: annotazioni %+v, attesa distruttiva", name, a)
		}
	}
	for _, name := range []string{"pr_list", "code_file", "issue_view"} {
		a := ls.Tools[by[name]].Annotations
		if a.DestructiveHint || !a.ReadOnlyHint {
			t.Errorf("%s: annotazioni %+v, attesa lettura", name, a)
		}
	}
	if a := ls.Tools[by["issue_create"]].Annotations; a.DestructiveHint || a.ReadOnlyHint {
		t.Errorf("issue_create: %+v", a)
	}
	if !ls.Tools[by[ApiRequestName]].Annotations.DestructiveHint {
		t.Error("api_request può eliminare: deve essere annotato distruttivo")
	}
}

func TestChiamataRiuscita(t *testing.T) {
	c := newClient(t)
	a := c.send("tools/call", map[string]any{"name": "issue_list", "arguments": map[string]any{"owner": "alice", "repo": "web", "state": "open", "labels": []string{"bug", "ui"}}})
	b := c.send("tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"owner": "alice", "repo": "web", "body": map[string]any{"title": "Crash"}}})
	res := c.run(false)
	cr := decodeCall(t, res[a])
	if cr.IsError || len(cr.Content) != 1 || cr.Content[0].Text != `{"ok":true}` {
		t.Errorf("issue_list: %+v", cr)
	}
	if len(c.api.calls) != 2 {
		t.Fatalf("chiamate: %+v", c.api.calls)
	}
	g := c.api.calls[0]
	if g.method != "GET" || !strings.HasPrefix(g.path, "/repos/alice/web/issues?") || !strings.Contains(g.path, "state=open") {
		t.Errorf("GET: %+v", g)
	}
	if p := c.api.calls[1]; p.method != "POST" || p.path != "/repos/alice/web/issues" || p.body != `{"title":"Crash"}` {
		t.Errorf("POST: %+v", p)
	}
	if cr := decodeCall(t, res[b]); cr.IsError {
		t.Errorf("issue_create: %+v", cr)
	}
}

func TestErroriDellAPIEArgomenti(t *testing.T) {
	c := newClient(t)
	c.api.fail = 404
	a := c.send("tools/call", map[string]any{"name": "code_tree", "arguments": map[string]any{"owner": "alice", "repo": "web"}})
	b := c.send("tools/call", map[string]any{"name": "code_tree", "arguments": map[string]any{"owner": "alice"}})
	d := c.send("tools/call", map[string]any{"name": "code_tree", "arguments": map[string]any{"owner": "a", "repo": "b", "boh": 1}})
	e := c.send("tools/call", map[string]any{"name": "non_esiste"})
	res := c.run(false)
	if cr := decodeCall(t, res[a]); !cr.IsError || !strings.Contains(cr.Content[0].Text, "404") {
		t.Errorf("404: %+v", cr)
	}
	for _, id := range []int{b, d} {
		if cr := decodeCall(t, res[id]); !cr.IsError {
			t.Errorf("argomenti errati accettati: %+v", cr)
		}
	}
	if len(c.api.calls) != 1 {
		t.Errorf("gli argomenti errati non devono arrivare all'API: %+v", c.api.calls)
	}
	if res[e].Error == nil || res[e].Error.Code != codeInvalidParams {
		t.Errorf("strumento sconosciuto: %+v", res[e])
	}
}

func TestReadOnlyRifiutaLeScritture(t *testing.T) {
	c := newClient(t)
	l := c.send("tools/list", nil)
	w := c.send("tools/call", map[string]any{"name": "pr_merge", "arguments": map[string]any{"owner": "a", "repo": "b", "number": 1}})
	post := c.send("tools/call", map[string]any{"name": ApiRequestName, "arguments": map[string]any{"method": "POST", "path": "/repos/a/b/issues", "body": map[string]any{"title": "x"}}})
	del := c.send("tools/call", map[string]any{"name": ApiRequestName, "arguments": map[string]any{"method": "delete", "path": "/repos/a/b"}})
	get := c.send("tools/call", map[string]any{"name": ApiRequestName, "arguments": map[string]any{"path": "/api/v1/repos/a/b/issues", "query": map[string]any{"state": "open", "page": 2}}})
	rd := c.send("tools/call", map[string]any{"name": "pr_list", "arguments": map[string]any{"owner": "a", "repo": "b"}})
	res := c.run(true)

	var ls listed
	_ = json.Unmarshal(res[l].Result, &ls)
	for _, tl := range ls.Tools {
		if tl.Name == ApiRequestName {
			if !tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint {
				t.Errorf("api_request in sola lettura: %+v", tl.Annotations)
			}
			continue
		}
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s elencato in --read-only ma scrive", tl.Name)
		}
	}
	for _, id := range []int{w, post, del} {
		cr := decodeCall(t, res[id])
		if !cr.IsError || !strings.Contains(cr.Content[0].Text, "read-only") {
			t.Errorf("scrittura non rifiutata: %+v", cr)
		}
	}
	for _, id := range []int{get, rd} {
		if cr := decodeCall(t, res[id]); cr.IsError {
			t.Errorf("lettura rifiutata: %+v", cr)
		}
	}
	if len(c.api.calls) != 2 {
		t.Fatalf("all'API devono arrivare solo le due letture: %+v", c.api.calls)
	}
	for _, cl := range c.api.calls {
		if cl.method != "GET" {
			t.Errorf("chiamata non GET in sola lettura: %+v", cl)
		}
	}
	if p := c.api.calls[0].path; p != "/repos/a/b/issues?page=2&state=open" {
		t.Errorf("percorso di api_request: %q", p)
	}
}

func TestApiRequestPercorsiNonAmmessi(t *testing.T) {
	for _, p := range []string{"https://evil.example/x", "//evil/x", "/repos/../admin", "/orgs?x=1"} {
		c := newClient(t)
		id := c.send("tools/call", map[string]any{"name": ApiRequestName, "arguments": map[string]any{"path": p}})
		res := c.run(false)
		if cr := decodeCall(t, res[id]); !cr.IsError || len(c.api.calls) != 0 {
			t.Errorf("%q: %+v, chiamate %v", p, cr, c.api.calls)
		}
	}
}

// --- Contratto e strumenti non divergono; M3 ---

type specOp struct {
	id, method, path string
	expose           bool
	name             string
}

func specOps(t *testing.T) []specOp {
	t.Helper()
	raw, err := os.ReadFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var ops []specOp
	for path, item := range doc.Paths {
		for m, node := range item {
			if m == "parameters" {
				continue
			}
			var op struct {
				ID  string `yaml:"operationId"`
				MCP struct {
					Expose bool   `yaml:"expose"`
					Name   string `yaml:"name"`
				} `yaml:"x-mcp"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatal(err)
			}
			ops = append(ops, specOp{op.ID, strings.ToUpper(m), path, op.MCP.Expose, op.MCP.Name})
		}
	}
	return ops
}

func TestStrumentiCorrispondonoAlContratto(t *testing.T) {
	have := map[string]Tool{}
	for _, tl := range Tools {
		have[tl.Operation] = tl
	}
	exposed := 0
	for _, op := range specOps(t) {
		tl, ok := have[op.id]
		if op.expose != ok {
			t.Errorf("%s: expose=%v ma strumento presente=%v", op.id, op.expose, ok)
			continue
		}
		if !ok {
			continue
		}
		exposed++
		if tl.Name != op.name || tl.Method != op.method || tl.Path != op.path {
			t.Errorf("%s: strumento %+v diverge dal contratto %+v", op.id, tl, op)
		}
	}
	if exposed != len(Tools) {
		t.Errorf("strumenti %d, operazioni esposte %d", len(Tools), exposed)
	}
}

// M3: nessuno strumento dedicato per eliminare o archiviare repo, impostazioni,
// webhook, token e amministrazione.
func TestNessunoStrumentoPerLeOperazioniEscluse(t *testing.T) {
	banned := map[string]bool{
		"deleteRepository": true, "updateRepository": true, "restoreRepository": true, "importRepository": true,
		"getBranchSettings": true, "updateBranchSettings": true, "getAdminSystem": true, "deleteRepoWebhook": true,
		"createRepoWebhook": true, "createToken": true, "createUserToken": true, "revokeToken": true, "deleteUser": true,
		"deleteOrganization": true, "deleteRepoMirror": true, "stopRepoPullMirror": true, "createResourceGrant": true,
	}
	for _, tl := range Tools {
		if banned[tl.Operation] {
			t.Errorf("strumento %s per l'operazione esclusa %s", tl.Name, tl.Operation)
		}
		switch {
		case strings.HasPrefix(tl.Path, "/internal/"), strings.HasPrefix(tl.Path, "/admin"), strings.Contains(tl.Path, "/hooks"),
			strings.Contains(tl.Path, "/tokens"), strings.Contains(tl.Path, "/user/tokens"), strings.Contains(tl.Path, "/mirrors"),
			strings.Contains(tl.Path, "/pull-mirror"), strings.HasPrefix(tl.Path, "/users"), strings.HasPrefix(tl.Path, "/orgs"):
			t.Errorf("strumento %s su %s: M3 non lo prevede", tl.Name, tl.Path)
		}
		if tl.Method == "DELETE" && tl.Path == "/repos/{owner}/{repo}" {
			t.Errorf("strumento di eliminazione repo: %s", tl.Name)
		}
		if tl.Method == "DELETE" && !tl.Destructive {
			t.Errorf("%s: DELETE non distruttivo", tl.Name)
		}
		if tl.Method == "GET" && tl.Destructive {
			t.Errorf("%s: GET distruttivo", tl.Name)
		}
	}
	// E nemmeno in elenco.
	c := newClient(t)
	l := c.send("tools/list", nil)
	res := c.run(false)
	var ls listed
	_ = json.Unmarshal(res[l].Result, &ls)
	for _, tl := range ls.Tools {
		if strings.Contains(tl.Name, "webhook") || strings.Contains(tl.Name, "token") || strings.Contains(tl.Name, "repo_delete") || strings.Contains(tl.Name, "archive") {
			t.Errorf("strumento inatteso: %s", tl.Name)
		}
	}
	_ = fmt.Sprint
}
