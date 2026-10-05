package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// Con i modelli veri: .gitignore Go e licenza MIT finiscono nel primo commit.
func TestCreateConModelliVeri(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non trovato nel PATH")
	}
	const secret, id = "s", "0a1b2c3d-1111-4222-8333-444455556666"
	st, err := repostore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := httpserver.NewRouter(httpserver.Deps{Store: st, Content: newContent(), Secret: secret})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/git/repos", strings.NewReader(body))
		trust.Sign(req.Header, secret, trust.Identity{UserID: "u1", Username: "core"}, time.Now())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	a := `"author":{"name":"Alice","email":"a@example.com"}`
	if rec := post(`{"repoId":"` + id + `","name":"demo","gitignoreTemplate":"cobol",` + a + `}`); rec.Code != 400 {
		t.Fatalf("modello sconosciuto: %d %s", rec.Code, rec.Body)
	}
	rec := post(`{"repoId":"` + id + `","name":"demo","description":"d","readme":true,"gitignoreTemplate":"go","licenseTemplate":"mit","licenseHolder":"ACME",` + a + `}`)
	if rec.Code != 201 {
		t.Fatalf("creazione: %d %s", rec.Code, rec.Body)
	}
	p, _ := st.RepoPath(id)
	show := func(f string) string {
		out, err := exec.Command("git", "-C", p, "show", "main:"+f).CombinedOutput()
		if err != nil {
			t.Fatalf("show %s: %v: %s", f, err, out)
		}
		return string(out)
	}
	if got := show("README.md"); got != "# demo\n\nd\n" {
		t.Errorf("README = %q", got)
	}
	if got := show("LICENSE"); !strings.Contains(got, "ACME") || strings.Contains(got, "{{") {
		t.Errorf("LICENSE non compilata: %.200q", got)
	}
	if got := show(".gitignore"); strings.TrimSpace(got) == "" {
		t.Error(".gitignore vuoto")
	}
}
