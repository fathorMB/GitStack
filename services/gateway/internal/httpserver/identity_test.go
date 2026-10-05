package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/security"
)

// contractRoutes legge api/openapi.yaml (senza dipendenze YAML: il file ha
// una struttura regolare) e ritorna, per ogni percorso, i tag dell'operazione.
func contractRoutes(t *testing.T) map[string][]string {
	t.Helper()
	// La spec arriva dal modulo api (go run in modalità workspace), non da
	// un percorso relativo fuori dal modulo.
	specFile := filepath.Join(t.TempDir(), "openapi.yaml")
	if out, err := exec.Command("go", "run", "github.com/fathorMB/GitStack/api/cmd/specdump", specFile).CombinedOutput(); err != nil {
		t.Fatalf("specdump: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("lettura della spec: %v", err)
	}
	pathRe := regexp.MustCompile(`^  (/[^\s:]*):\s*$`)
	tagsRe := regexp.MustCompile(`^\s+tags:\s*\[([^\]]*)\]`)
	routes := map[string][]string{}
	cur := ""
	inPaths := false
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "paths:") {
			inPaths = true
			continue
		}
		if inPaths && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break // fine di "paths:"
		}
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if m := tagsRe.FindStringSubmatch(line); m != nil && cur != "" {
			for _, tag := range strings.Split(m[1], ",") {
				routes[cur] = append(routes[cur], strings.TrimSpace(tag))
			}
		}
	}
	if len(routes) == 0 {
		t.Fatal("nessun percorso letto da api/openapi.yaml")
	}
	return routes
}

var identityTags = map[string]bool{
	"auth": true, "users": true, "tokens": true, "ssh-keys": true,
	"organizations": true, "teams": true, "permissions": true,
}

// TestIdentityRoutes_SecondoIlContratto: ogni percorso di openapi.yaml con
// un tag di identity arriva a identity (e non a core), col prefisso /v1
// rimosso; i percorsi con tag internal non sono esposti (404, nessuno dei
// due servizi li vede).
func TestIdentityRoutes_SecondoIlContratto(t *testing.T) {
	var identityPaths, corePaths []string
	fakeIdentity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityPaths = append(identityPaths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fakeIdentity.Close()
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corePaths = append(corePaths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fakeCore.Close()

	cfg := newTestConfig(t, fakeCore.URL)
	cfg.IdentityURL = mustURL(t, fakeIdentity.URL)
	cfg.IdentityTimeout = cfg.CoreTimeout
	router := NewRouter(cfg, discardLogger(), WithVerifier(allowAll()))
	table, err := security.NewTable(security.Routes)
	if err != nil {
		t.Fatal(err)
	}

	param := regexp.MustCompile(`\{[^}]+\}`)
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}

	for path, tags := range contractRoutes(t) {
		concrete := param.ReplaceAllString(path, "x")
		switch {
		case hasAny(tags, identityTags):
			for _, m := range methods {
				identityPaths, corePaths = nil, nil
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, authedRequestFor(table, m, "/v1"+concrete))
				if _, declared := table.Lookup(m, concrete); !declared {
					// Metodo non dichiarato dal contratto: il gateway non lo
					// inoltra a identity.
					if rec.Code != http.StatusNotFound || len(identityPaths)+len(corePaths) != 0 {
						t.Errorf("%s /v1%s non dichiarata: status %d, a valle identity %v core %v; atteso 404 senza inoltro", m, concrete, rec.Code, identityPaths, corePaths)
					}
					continue
				}
				if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
					t.Errorf("%s /v1%s: status %d, atteso l'instradamento verso identity", m, concrete, rec.Code)
				}
				if len(identityPaths) != 1 || identityPaths[0] != concrete || len(corePaths) != 0 {
					t.Errorf("%s /v1%s: identity ha visto %v, core %v; atteso identity [%s]", m, concrete, identityPaths, corePaths, concrete)
				}
			}
		case hasAny(tags, map[string]bool{"internal": true, "git-internal": true}):
			for _, m := range methods {
				identityPaths, corePaths = nil, nil
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, authedRequest(m, "/v1"+concrete))
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s /v1%s: status %d, /internal/* non va esposto (atteso 404)", m, concrete, rec.Code)
				}
				if len(identityPaths)+len(corePaths) != 0 {
					t.Errorf("%s /v1%s è arrivato a un servizio a valle", m, concrete)
				}
			}
		}
	}
}

// TestIdentityRoutes_InternalNonEsposto: anche senza passare dal
// prefisso /v1 e con percorsi vicini, /internal/* resta chiuso.
func TestIdentityRoutes_InternalNonEsposto(t *testing.T) {
	var seen int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()
	cfg := newTestConfig(t, fake.URL)
	cfg.IdentityURL = mustURL(t, fake.URL)
	router := NewRouter(cfg, discardLogger(), WithVerifier(allowAll()))

	for _, p := range []string{"/v1/internal/verify", "/internal/verify", "/v1/internal/permissions/check", "/v1/internal/ssh-keys/abc", "/v1/auth/../internal/verify"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, authedRequest(http.MethodPost, p))
		if rec.Code == http.StatusNoContent || seen != 0 {
			t.Errorf("POST %s ha raggiunto un servizio a valle (status %d)", p, rec.Code)
		}
	}
}

// TestIdentityRoutes_SenzaIdentityUrl: senza GITSTACK_IDENTITY_URL le rotte
// di identity non sono montate.
func TestIdentityRoutes_SenzaIdentityUrl(t *testing.T) {
	router := NewRouter(newTestConfig(t, "http://127.0.0.1:1"), discardLogger(), WithVerifier(allowAll()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodGet, "/v1/auth/session"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404 senza identity configurata", rec.Code)
	}
}

// TestIdentityRoutes_IdentityGiu: identity irraggiungibile -> 503 con code
// identity_unavailable (risposta del gateway, non di core), anche per la
// verifica della credenziale: mai fail open.
func TestIdentityRoutes_IdentityGiu(t *testing.T) {
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	cfg.IdentityURL = mustURL(t, "http://127.0.0.1:1")
	cfg.IdentityTimeout = cfg.CoreTimeout
	cfg.IdentityServiceSecret = testSecret
	router := NewRouter(cfg, discardLogger())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodGet, "/v1/auth/session"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "identity_unavailable") {
		t.Fatalf("status = %d, corpo = %s; atteso 503 identity_unavailable", rec.Code, rec.Body.String())
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func hasAny(tags []string, set map[string]bool) bool {
	for _, tag := range tags {
		if set[tag] {
			return true
		}
	}
	return false
}
