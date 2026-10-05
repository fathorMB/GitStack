package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

const (
	secret = "segreto"
	rid    = "0a1b2c3d-1111-4222-8333-444455556666"
)

type fakeContent struct{}

func (fakeContent) Gitignore(id string) ([]byte, error) {
	if id != "go" {
		return nil, ErrUnknownTemplate
	}
	return []byte("*.exe\n"), nil
}

func (fakeContent) License(id string, year int, holder string) ([]byte, error) {
	if id != "mit" {
		return nil, ErrUnknownTemplate
	}
	return []byte("MIT " + holder + "\n"), nil
}

func (fakeContent) Readme(name, _ string) []byte { return []byte("# " + name + "\n") }

type env struct {
	t     *testing.T
	root  string
	h     http.Handler
	store *repostore.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non trovato nel PATH")
	}
	root := t.TempDir()
	st, err := repostore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, root: root, store: st, h: NewRouter(Deps{Store: st, Content: fakeContent{}, Secret: secret})}
}

func (e *env) do(method, path, body string, signed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if signed {
		trust.Sign(req.Header, secret, trust.Identity{UserID: "u1", Username: "core"}, time.Now())
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) expect(rec *httptest.ResponseRecorder, code int) {
	e.t.Helper()
	if rec.Code != code {
		e.t.Fatalf("status = %d, voluto %d: %s", rec.Code, code, rec.Body)
	}
}

func TestSenzaFirma_401(t *testing.T) {
	e := setup(t)
	for _, c := range [][2]string{
		{"POST", "/internal/git/repos"},
		{"GET", "/internal/git/repos/" + rid},
		{"POST", "/internal/git/repos/" + rid + "/trash"},
		{"POST", "/internal/git/repos/" + rid + "/restore"},
		{"DELETE", "/internal/git/repos/" + rid},
	} {
		e.expect(e.do(c[0], c[1], `{}`, false), 401)
	}
	// Firma con un altro segreto, e timestamp scaduto.
	req := httptest.NewRequest("GET", "/internal/git/repos/"+rid, nil)
	trust.Sign(req.Header, "altro", trust.Identity{UserID: "u1", Username: "core"}, time.Now())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 401)
	req = httptest.NewRequest("GET", "/internal/git/repos/"+rid, nil)
	trust.Sign(req.Header, secret, trust.Identity{UserID: "u1", Username: "core"}, time.Now().Add(-time.Hour))
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	e.expect(rec, 401)
	// Niente repo creato da chiamate rifiutate.
	e.expect(e.do("GET", "/internal/git/repos/"+rid, "", true), 404)
}

func TestSenzaSegretoConfigurato_401(t *testing.T) {
	e := setup(t)
	h := NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: ""})
	req := httptest.NewRequest("GET", "/internal/git/repos/"+rid, nil)
	trust.Sign(req.Header, "", trust.Identity{UserID: "u1", Username: "core"}, time.Now())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProbe(t *testing.T) {
	e := setup(t)
	e.expect(e.do("GET", "/healthz", "", false), 200)
	e.expect(e.do("GET", "/readyz", "", false), 200)

	bad, _ := repostore.NewWithGit(e.root+"/manca", "git")
	h := NewRouter(Deps{Store: bad, Content: fakeContent{}, Secret: secret})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readyz con directory mancante = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestCicloDiVita(t *testing.T) {
	e := setup(t)
	rec := e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"demo"}`, true)
	e.expect(rec, 201)
	var out struct {
		RepoID string `json:"repoId"`
		Empty  bool   `json:"empty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.RepoID != rid || !out.Empty {
		t.Fatalf("risposta: %s", rec.Body)
	}
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"demo"}`, true), 409)

	rec = e.do("GET", "/internal/git/repos/"+rid, "", true)
	e.expect(rec, 200)
	if !strings.Contains(rec.Body.String(), `"trashed":false`) || !strings.Contains(rec.Body.String(), `"empty":true`) {
		t.Fatalf("get: %s", rec.Body)
	}

	// Purge di un repo non nel cestino: 409.
	e.expect(e.do("DELETE", "/internal/git/repos/"+rid, "", true), 409)
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/restore", "", true), 409)
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/trash", "", true), 204)
	if rec = e.do("GET", "/internal/git/repos/"+rid, "", true); !strings.Contains(rec.Body.String(), `"trashed":true`) {
		t.Fatalf("get dopo trash: %s", rec.Body)
	}
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/restore", "", true), 204)
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/trash", "", true), 204)
	e.expect(e.do("DELETE", "/internal/git/repos/"+rid, "", true), 204)
	e.expect(e.do("GET", "/internal/git/repos/"+rid, "", true), 404)
	e.expect(e.do("DELETE", "/internal/git/repos/"+rid, "", true), 404)
}

func TestCreateConContenuto(t *testing.T) {
	e := setup(t)
	body := `{"repoId":"` + rid + `","name":"demo","readme":true,"gitignoreTemplate":"go","licenseTemplate":"mit","author":{"name":"Alice","email":"a@example.com"}}`
	rec := e.do("POST", "/internal/git/repos", body, true)
	e.expect(rec, 201)
	if !strings.Contains(rec.Body.String(), `"empty":false`) {
		t.Fatalf("risposta: %s", rec.Body)
	}
	p, _ := e.store.RepoPath(rid)
	out, err := exec.Command("git", "-C", p, "ls-tree", "--name-only", "main").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	for _, f := range []string{"README.md", ".gitignore", "LICENSE"} {
		if !strings.Contains(string(out), f) {
			t.Fatalf("manca %s: %s", f, out)
		}
	}
	// Il titolare della licenza è, di default, l'autore.
	show, _ := exec.Command("git", "-C", p, "show", "main:LICENSE").Output()
	if string(show) != "MIT Alice\n" {
		t.Fatalf("LICENSE = %q", show)
	}
}

func TestCreate400(t *testing.T) {
	e := setup(t)
	a := `"author":{"name":"A","email":"a@b.c"}`
	for name, body := range map[string]string{
		"json":         `{`,
		"uuid":         `{"repoId":"nope","name":"x"}`,
		"nome":         `{"repoId":"` + rid + `"}`,
		"gitignore":    `{"repoId":"` + rid + `","name":"x","gitignoreTemplate":"cobol",` + a + `}`,
		"licenza":      `{"repoId":"` + rid + `","name":"x","licenseTemplate":"boh",` + a + `}`,
		"senza autore": `{"repoId":"` + rid + `","name":"x","readme":true}`,
		"branch":       `{"repoId":"` + rid + `","name":"x","defaultBranch":"a..b"}`,
	} {
		rec := e.do("POST", "/internal/git/repos", body, true)
		if rec.Code != 400 {
			t.Errorf("%s: status = %d: %s", name, rec.Code, rec.Body)
		}
	}
	e.expect(e.do("GET", "/internal/git/repos/"+rid, "", true), 404)
	e.expect(e.do("GET", "/internal/git/repos/non-uuid", "", true), 400)
	e.expect(e.do("POST", "/internal/git/repos/non-uuid/trash", "", true), 400)
}
