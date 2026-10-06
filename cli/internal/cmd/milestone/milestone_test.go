package milestone_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type call struct {
	Method, Path, Query string
	Body                map[string]any
}

type reply struct {
	status int
	body   string
}

type fake struct {
	mu      sync.Mutex
	routes  map[string][]reply
	calls   []call
	srv     *httptest.Server
	callIdx map[string]int
}

func newFake(t *testing.T, routes map[string][]reply) *fake {
	t.Helper()
	fk := &fake{routes: routes, callIdx: map[string]int{}}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		key := r.Method + " " + path
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, r.URL.RawQuery, body})
		fk.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"serve autenticazione"}}`)
			return
		}
		rep, ok := fk.routes[key]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"non trovato"}}`)
			return
		}
		n := len(rep)
		idx := fk.callIdx[key] % n
		fk.callIdx[key]++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep[idx].status)
		_, _ = io.WriteString(w, rep[idx].body)
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

func (fk *fake) find(method, path string) []call {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	var out []call
	for _, c := range fk.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	code        int
	out, errOut string
}

func run(t *testing.T, fk *fake, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", fk.srv.URL, "-R", "acme/web"}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

const (
	milestoneJSON = `{"id":"00000000-0000-0000-0000-000000000001","number":1,"title":"v1.0","description":"Versione 1.0","state":"open","openIssues":2,"closedIssues":1}`
)

func TestList(t *testing.T) {
	list := `{"items":[` + milestoneJSON + `],"page":1,"perPage":30,"total":1}`
	fk := newFake(t, map[string][]reply{"GET /repos/acme/web/milestones": {{200, list}}})
	r := run(t, fk, "", "milestone", "list")
	if r.code != 0 || !strings.Contains(r.out, "v1.0") {
		t.Fatalf("exit %d: %q | %s", r.code, r.out, r.errOut)
	}
}

func TestList_StateAll(t *testing.T) {
	list := `{"items":[` + milestoneJSON + `],"page":1,"perPage":30,"total":1}`
	fk := newFake(t, map[string][]reply{"GET /repos/acme/web/milestones": {{200, list}}})
	r := run(t, fk, "", "milestone", "list", "--state", "all")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
}

func TestList_Vuoto(t *testing.T) {
	fk := newFake(t, map[string][]reply{"GET /repos/acme/web/milestones": {{200, `{"items":[],"page":1,"perPage":30,"total":0}`}}})
	r := run(t, fk, "", "milestone", "list")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
}

func TestView(t *testing.T) {
	fk := newFake(t, map[string][]reply{"GET /repos/acme/web/milestones/1": {{200, milestoneJSON}}})
	r := run(t, fk, "", "milestone", "view", "1")
	if r.code != 0 || !strings.Contains(r.out, "v1.0") {
		t.Fatalf("view: exit %d: %s", r.code, r.errOut)
	}
	if r = run(t, fk, "", "milestone", "view", "1", "--json", "number,title"); r.code != 0 || !strings.Contains(r.out, `"v1.0"`) {
		t.Fatalf("view json: %+v", r)
	}
}

func TestCreate(t *testing.T) {
	fk := newFake(t, map[string][]reply{
		"POST /repos/acme/web/milestones": {{201, milestoneJSON}},
	})
	r := run(t, fk, "", "milestone", "create", "v2.0", "--description", "Versione 2", "--due-on", "2026-12-01")
	if r.code != 0 {
		t.Fatalf("create: exit %d: %s", r.code, r.errOut)
	}
	b := fk.find("POST", "/repos/acme/web/milestones")[0].Body
	if b["title"] != "v2.0" || b["description"] != "Versione 2" {
		t.Errorf("corpo: %v", b)
	}
	for _, args := range [][]string{
		{"milestone", "create"},
		{"milestone", "create", "v3", "--due-on", "bad-date"},
	} {
		if r := run(t, fk, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
}

func TestEdit(t *testing.T) {
	fk := newFake(t, map[string][]reply{
		"PATCH /repos/acme/web/milestones/1": {{200, milestoneJSON}},
	})
	r := run(t, fk, "", "milestone", "edit", "1", "--title", "v1.1")
	if r.code != 0 || !strings.Contains(r.errOut, "Modificata") {
		t.Fatalf("edit: exit %d: %s", r.code, r.errOut)
	}
	b := fk.find("PATCH", "/repos/acme/web/milestones/1")[0].Body
	if b["title"] != "v1.1" {
		t.Errorf("corpo: %v", b)
	}
	for _, args := range [][]string{
		{"milestone", "edit", "1"},
		{"milestone", "edit", "1", "--due-on", "bad", "--remove-due-on"},
	} {
		if r := run(t, fk, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
}

func TestCloseReopen(t *testing.T) {
	closedMs := strings.Replace(milestoneJSON, `"state":"open"`, `"state":"closed"`, 1)
	openMs := strings.Replace(milestoneJSON, `"state":"closed"`, `"state":"open"`, 1)
	fk := newFake(t, map[string][]reply{
		"PATCH /repos/acme/web/milestones/1": {{200, closedMs}, {200, openMs}},
	})
	r := run(t, fk, "", "milestone", "close", "1")
	if r.code != 0 || !strings.Contains(r.errOut, "Chiusa") {
		t.Fatalf("close: exit %d: %s", r.code, r.errOut)
	}
	r = run(t, fk, "", "milestone", "reopen", "1")
	if r.code != 0 || !strings.Contains(r.errOut, "Riaperta") {
		t.Fatalf("reopen: exit %d: %s", r.code, r.errOut)
	}
}

func TestDelete_ConfermeG8(t *testing.T) {
	fk := newFake(t, map[string][]reply{"DELETE /repos/acme/web/milestones/1": {{204, ``}}})
	// Senza TTY e senza --yes: exit 2, zero DELETE.
	if r := run(t, fk, "", "milestone", "delete", "1"); r.code != 2 || len(fk.find("DELETE", "/repos/acme/web/milestones/1")) != 0 {
		t.Errorf("senza tty: exit %d", r.code)
	}
	// --yes senza TTY: elimina.
	r := run(t, fk, "", "milestone", "delete", "1", "--yes")
	if r.code != 0 || len(fk.find("DELETE", "/repos/acme/web/milestones/1")) != 1 {
		t.Errorf("--yes: exit %d (%s)", r.code, r.errOut)
	}
}
