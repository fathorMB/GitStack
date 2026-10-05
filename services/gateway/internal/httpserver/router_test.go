package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestConfig(t *testing.T, coreURL string) config.Config {
	t.Helper()
	u, err := url.Parse(coreURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", coreURL, err)
	}
	return config.Config{
		Addr:        ":0",
		CoreURL:     u,
		CoreTimeout: time.Second,
		LogLevel:    "info",
	}
}

// TestRouter_Healthz_SempreOk verifica il criterio di liveness: /healthz
// risponde 200 anche se core non è raggiungibile, perché la liveness non
// dipende dalle risorse a valle.
func TestRouter_Healthz_SempreOk(t *testing.T) {
	cfg := newTestConfig(t, "http://127.0.0.1:1") // nessun core in ascolto
	router := NewRouter(cfg, discardLogger())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("manca X-Request-Id sulla risposta di /healthz")
	}
}

// TestRouter_Readyz_ConCoreFinto è il test d'integrazione con un core finto
// (httptest.Server): readiness ok quando core risponde, non ok quando core
// non è raggiungibile.
func TestRouter_Readyz_ConCoreFinto(t *testing.T) {
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.1.0"}`))
	}))
	defer fakeCore.Close()

	router := NewRouter(newTestConfig(t, fakeCore.URL), discardLogger())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz con core su = %d, voluto 200", rec.Code)
	}

	fakeCore.Close()

	routerCoreGiu := NewRouter(newTestConfig(t, fakeCore.URL), discardLogger())
	rec2 := httptest.NewRecorder()
	routerCoreGiu.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz con core giù = %d, voluto 503", rec2.Code)
	}
}

// TestRouter_APIInstradataVersoCore è il test d'integrazione principale:
// una richiesta /v1/* arriva al router del gateway con la catena completa
// (request id, log, auth/rate-limit no-op) ed esce instradata verso un core
// finto, con il request id propagato.
func TestRouter_APIInstradataVersoCore(t *testing.T) {
	var gotPath, gotRequestID string
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRequestID = r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.1.0"}`))
	}))
	defer fakeCore.Close()

	router := NewRouter(newTestConfig(t, fakeCore.URL), discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if gotPath != "/health" {
		t.Errorf("path visto da core = %q, voluto /health", gotPath)
	}
	if gotRequestID == "" {
		t.Error("il request id non è arrivato a core")
	}
	if resp := rec.Header().Get("X-Request-Id"); resp != gotRequestID {
		t.Errorf("request id sulla risposta = %q, voluto uguale a quello visto da core %q", resp, gotRequestID)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non JSON valido: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("corpo = %v, voluto status=ok", body)
	}
}

// TestRouter_ResourcesInstradato verifica che anche una rotta con parametri
// (/v1/resources) sia instradata, non solo /v1/health.
func TestRouter_ResourcesInstradato(t *testing.T) {
	var gotPath, gotQuery string
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[],"page":1,"perPage":20,"total":0}`))
	}))
	defer fakeCore.Close()

	router := NewRouter(newTestConfig(t, fakeCore.URL), discardLogger(), WithVerifier(allowAll()))

	req := authedRequest(http.MethodGet, "/v1/resources?type=repo")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if gotPath != "/resources" {
		t.Errorf("path visto da core = %q, voluto /resources", gotPath)
	}
	if gotQuery != "type=repo" {
		t.Errorf("query vista da core = %q, voluta type=repo", gotQuery)
	}
}

// TestRouter_ReposInstradatiVersoCore verifica le rotte del tag `repos`
// (M-03): tutte a core, e GET /v1/repos/deleted (2 segmenti) non finisce su
// GET /v1/repos/{owner}/{repo} (3 segmenti). Le rotte /v1/internal/git/*
// non sono esposte.
func TestRouter_ReposInstradatiVersoCore(t *testing.T) {
	var gotMethod, gotPath string
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fakeCore.Close()
	router := NewRouter(newTestConfig(t, fakeCore.URL), discardLogger(), WithVerifier(allowAll()))

	id := "22222222-2222-2222-2222-222222222222"
	cases := []struct{ method, target, wantPath string }{
		{http.MethodGet, "/v1/repos?owner=alice&page=2", "/repos"},
		{http.MethodPost, "/v1/repos", "/repos"},
		{http.MethodGet, "/v1/repos/deleted?owner=alice", "/repos/deleted"},
		{http.MethodPost, "/v1/repos/deleted/" + id + "/restore", "/repos/deleted/" + id + "/restore"},
		{http.MethodGet, "/v1/repos/alice/my-app", "/repos/alice/my-app"},
		{http.MethodPatch, "/v1/repos/alice/my-app", "/repos/alice/my-app"},
		{http.MethodDelete, "/v1/repos/alice/my-app", "/repos/alice/my-app"},
	}
	for _, c := range cases {
		gotMethod, gotPath = "", ""
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, authedRequest(c.method, c.target))
		if gotMethod != c.method || gotPath != c.wantPath {
			t.Errorf("%s %s: core ha visto %s %s, atteso %s %s (status %d)", c.method, c.target, gotMethod, gotPath, c.method, c.wantPath, rec.Code)
		}
	}

	gotPath = ""
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodPost, "/v1/internal/git/repos"))
	if rec.Code != http.StatusNotFound || gotPath != "" {
		t.Errorf("/v1/internal/git/repos: status %d, core %q; atteso 404 senza inoltro", rec.Code, gotPath)
	}
}
