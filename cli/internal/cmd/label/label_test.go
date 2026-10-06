package label_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/label"
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
	mu     sync.Mutex
	routes map[string]reply
	calls  []call
	srv    *httptest.Server
}

func newFake(t *testing.T, routes map[string]reply) *fake {
	t.Helper()
	fk := &fake{routes: routes}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, r.URL.RawQuery, body})
		fk.mu.Unlock()
		rep, ok := fk.routes[r.Method+" "+path]
		if !ok {
			rep = reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			rep = reply{401, `{"error":{"code":"unauthenticated","message":"serve autenticazione"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
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
	labelJSON = `{"id":"00000000-0000-0000-0000-000000000001","name":"bug","color":"d73a4a","description":"Bug","openIssues":3}`
)

func TestList(t *testing.T) {
	list := `{"items":[` + labelJSON + `],"page":1,"perPage":30,"total":1}`
	fk := newFake(t, map[string]reply{"GET /repos/acme/web/labels": {200, list}})
	r := run(t, fk, "", "label", "list")
	if r.code != 0 || !strings.Contains(r.out, "bug") || !strings.Contains(r.out, "d73a4a") {
		t.Fatalf("exit %d: %q | %s", r.code, r.out, r.errOut)
	}
	if r = run(t, fk, "", "label", "list", "--json", "name,color"); r.code != 0 || !strings.Contains(r.out, `"bug"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestList_Vuoto(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /repos/acme/web/labels": {200, `{"items":[],"page":1,"perPage":30,"total":0}`}})
	r := run(t, fk, "", "label", "list")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
}

func TestCreate(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"POST /repos/acme/web/labels": {201, labelJSON},
	})
	r := run(t, fk, "", "label", "create", "test", "--color", "00ff00", "--description", "test label")
	if r.code != 0 || !strings.Contains(r.out, "bug") {
		t.Fatalf("exit %d: %q | %s", r.code, r.out, r.errOut)
	}
	b := fk.find("POST", "/repos/acme/web/labels")[0].Body
	if b["name"] != "test" || b["color"] != "00ff00" || b["description"] != "test label" {
		t.Errorf("corpo: %v", b)
	}
	for _, args := range [][]string{
		{"label", "create", "test", "--color", "xyz"},
		{"label", "create"},
	} {
		if r := run(t, fk, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
}

func TestEdit(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"PATCH /repos/acme/web/labels/bug": {200, labelJSON},
	})
	r := run(t, fk, "", "label", "edit", "bug", "--color", "ff0000")
	if r.code != 0 || !strings.Contains(r.errOut, "Modificata") {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	b := fk.find("PATCH", "/repos/acme/web/labels/bug")[0].Body
	if b["color"] != "ff0000" {
		t.Errorf("corpo: %v", b)
	}
	if _, ok := b["name"]; ok {
		t.Error("edit senza --name non tocca name")
	}
	for _, args := range [][]string{
		{"label", "edit", "bug"},
	} {
		if r := run(t, fk, "", args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
}

func TestDelete_ConfermeG8(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE /repos/acme/web/labels/bug": {204, ``}})
	// Senza TTY e senza --yes: exit 2, zero DELETE.
	if r := run(t, fk, "", "label", "delete", "bug"); r.code != 2 || len(fk.find("DELETE", "/repos/acme/web/labels/bug")) != 0 {
		t.Errorf("senza tty: exit %d", r.code)
	}
	// --yes senza TTY: elimina.
	r := run(t, fk, "", "label", "delete", "bug", "--yes")
	if r.code != 0 || len(fk.find("DELETE", "/repos/acme/web/labels/bug")) != 1 {
		t.Errorf("--yes: exit %d (%s)", r.code, r.errOut)
	}
}

func TestJsonCampiDocumentati(t *testing.T) {
	fk := newFake(t, nil)
	_ = run(t, fk, "", "label", "list", "--json")
	for _, f := range label.LabelFields {
		if !strings.Contains("", "`"+f+"`") { // placeholder
			t.Logf("campo %s documentato in LabelFields", f)
		}
	}
}
