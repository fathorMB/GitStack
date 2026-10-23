package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

type marker string

func (m marker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Handler", string(m))
	w.WriteHeader(http.StatusTeapot)
}

func newRegistryEnv(t *testing.T, d Deps) (*env, http.Handler) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non trovato nel PATH")
	}
	root := t.TempDir()
	st, err := repostore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	d.Store, d.Content, d.Secret = st, fakeContent{}, secret
	h := NewRouter(d)
	return &env{t: t, root: root, store: st, h: h}, h
}

// /v2/ è del registry (anche per un owner chiamato «v2»), /internal/ e smart HTTP restano come prima.
func TestRegistry_MontatoSuV2(t *testing.T) {
	_, h := newRegistryEnv(t, Deps{Registry: marker("registry"), Git: marker("git")})
	for path, want := range map[string]string{
		"/v2":                            "registry",
		"/v2/":                           "registry",
		"/v2/alice/app/manifests/latest": "registry",
		"/alice/app.git/info/refs":       "git",
		"/v2x/app.git/info/refs":         "git",
		"/v2/app.git/info/refs":          "registry",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := rec.Header().Get("X-Handler"); got != want {
			t.Errorf("%s servito da %q, atteso %q", path, got, want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/git/repos/"+rid, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/internal/ senza firma = %d", rec.Code)
	}
}

func TestRegistry_SpentoDa404(t *testing.T) {
	_, h := newRegistryEnv(t, Deps{Git: marker("git")})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v2/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/v2/ senza registry = %d", rec.Code)
	}
}

// La cancellazione definitiva toglie anche le immagini, ma solo dopo aver cancellato il repo,
// e un errore sulle immagini non la fa fallire.
func TestPurge_ToglieLeImmagini(t *testing.T) {
	var purged []string
	var failWith error
	e, _ := newRegistryEnv(t, Deps{})
	h := NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Registry: marker("r"),
		RegistryPurge: func(_ context.Context, id string) error { purged = append(purged, id); return failWith }})
	e.h = h

	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"app"}`, true), 201)

	// Non è nel cestino: Purge del repo fallisce, le immagini non si toccano.
	e.expect(e.do("DELETE", "/internal/git/repos/"+rid, "", true), 409)
	if len(purged) != 0 {
		t.Fatalf("immagini cancellate con il repo ancora attivo: %v", purged)
	}
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/trash", "", true), 204)
	e.expect(e.do("DELETE", "/internal/git/repos/"+rid, "", true), 204)
	if strings.Join(purged, ",") != rid {
		t.Fatalf("immagini cancellate = %v", purged)
	}

	// Errore sulle immagini: il repo è già andato, la risposta resta 204.
	id2 := "0a1b2c3d-1111-4222-8333-444455557777"
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+id2+`","name":"altro"}`, true), 201)
	e.expect(e.do("POST", "/internal/git/repos/"+id2+"/trash", "", true), 204)
	failWith = errors.New("disco pieno")
	e.expect(e.do("DELETE", "/internal/git/repos/"+id2, "", true), 204)
	if len(purged) != 2 {
		t.Errorf("immagini cancellate = %v", purged)
	}
}
