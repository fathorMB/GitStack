package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type seenReq struct {
	method, path, auth, xGitstack, cookie, xff string
	bodyLen                                    int
}

// fakeRegistry è il servizio che ospita il registry: registra ciò che gli arriva.
type fakeRegistry struct {
	mu   sync.Mutex
	seen []seenReq
	srv  *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Gitstack-User-Id"), r.Header.Get("Cookie"), r.Header.Get("X-Forwarded-For"), int(n)})
		f.mu.Unlock()
		w.Header().Set("Cache-Control", "max-age=31536000")
		w.Header().Set("WWW-Authenticate", `Basic realm="GitStack registry"`)
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func registryRouter(t *testing.T, registryURL string) http.Handler {
	t.Helper()
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	if registryURL != "" {
		u, err := url.Parse(registryURL)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RegistryURL = u
	}
	cfg.CoreTimeout = 50 * time.Millisecond
	return NewRouter(cfg, discardLogger())
}

// /v2/ passa dal gateway al registry con le credenziali intatte e senza i limiti dell'API JSON.
func TestRegistry_ProxyConCredenzialiESenzaLimiti(t *testing.T) {
	f := newFakeRegistry(t)
	router := registryRouter(t, f.srv.URL)

	// Un corpo da 5 MiB (oltre il tetto di 1 MiB dell'API) arriva intero, e Authorization
	// non si perde; ogni X-Gitstack-* del client e i cookie vengono tolti.
	body := bytes.Repeat([]byte("x"), 5<<20)
	req := httptest.NewRequest(http.MethodPatch, "/v2/alice/app/blobs/uploads/abc", bytes.NewReader(body))
	req.SetBasicAuth("docker", "gst_token")
	req.Header.Set("X-Gitstack-User-Id", "00000000-0000-0000-0000-000000000000")
	req.Header.Set("Cookie", "gst_session=segreta")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d: la risposta del registry va inoltrata così com'è", rec.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 1 {
		t.Fatalf("richieste al registry: %d", len(f.seen))
	}
	got := f.seen[0]
	if got.bodyLen != len(body) || got.path != "/v2/alice/app/blobs/uploads/abc" || got.method != "PATCH" {
		t.Errorf("richiesta inoltrata: %+v", got)
	}
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Errorf("Authorization perso: %q", got.auth)
	}
	if got.xGitstack != "" || got.cookie != "" {
		t.Errorf("X-Gitstack-* o cookie del client arrivati al registry: %+v", got)
	}
	if got.xff == "" {
		t.Error("manca X-Forwarded-For")
	}
	if rec.Header().Get("WWW-Authenticate") == "" || rec.Header().Get("Docker-Distribution-API-Version") == "" {
		t.Errorf("sfida e versione del registry non inoltrate: %v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || len(rec.Header().Values("Cache-Control")) != 1 {
		t.Errorf("Cache-Control = %v, atteso no-store (una volta)", rec.Header().Values("Cache-Control"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("manca nosniff: %v", rec.Header())
	}
}

func TestRegistry_BaseSenzaSlash(t *testing.T) {
	f := newFakeRegistry(t)
	router := registryRouter(t, f.srv.URL)
	for _, p := range []string{"/v2", "/v2/"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
}

// Senza registry configurato /v2/ non esiste; con il registry giù la risposta è nel formato OCI.
func TestRegistry_NonMontatoESpento(t *testing.T) {
	rec := httptest.NewRecorder()
	registryRouter(t, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("senza RegistryURL = %d, atteso 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	registryRouter(t, "http://127.0.0.1:1").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"errors"`) || !strings.Contains(rec.Body.String(), "UNAVAILABLE") {
		t.Errorf("registry spento: %d %s", rec.Code, rec.Body)
	}
}

// L'API JSON resta com'era: nessuna richiesta del gateway a /v1/ arriva al registry.
func TestRegistry_NonToccaV1(t *testing.T) {
	f := newFakeRegistry(t)
	router := registryRouter(t, f.srv.URL)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/repos/alice/app", nil))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 0 {
		t.Errorf("una richiesta a /v1/ è arrivata al registry: %+v", f.seen)
	}
}
