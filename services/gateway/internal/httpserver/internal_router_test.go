package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/appauth"
	"github.com/fathorMB/GitStack/services/gateway/internal/config"
)

// La porta interna (GIT-442, H16) non si fida di X-Forwarded-For nemmeno da un peer della rete dei
// pod: l'IP del client è quello del peer, quindi cambiare l'intestazione non aggira il limite per IP.
func TestInternalRouter_XForwardedForIgnorato(t *testing.T) {
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	_, trusted, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProxies = []*net.IPNet{trusted}
	cfg.RateLimit = config.RateLimitConfig{IPRate: 0.001, IPBurst: 1}

	// Controprova: la porta pubblica, con lo stesso peer, si fida di X-Forwarded-For.
	pub := NewRouter(cfg, discardLogger())
	if rec := metaFrom(pub, "10.42.0.7:4000", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Fatalf("pubblica, client 1: %d", rec.Code)
	}
	if rec := metaFrom(pub, "10.42.0.7:4000", "203.0.113.2"); rec.Code != http.StatusOK {
		t.Fatalf("pubblica, client 2 deve avere un bucket suo: %d", rec.Code)
	}

	in := NewInternalRouter(cfg, discardLogger())
	if rec := metaFrom(in, "10.42.0.7:4000", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Fatalf("interna, primo: %d", rec.Code)
	}
	if rec := metaFrom(in, "10.42.0.7:4000", "203.0.113.2"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("interna: X-Forwarded-For falsificato da un'app ha cambiato il bucket: %d, voluto 429", rec.Code)
	}
	// Un altro peer ha il suo bucket: l'IP è quello della connessione.
	if rec := metaFrom(in, "10.42.0.8:4000", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Errorf("interna, altro peer: %d, voluto 200", rec.Code)
	}
}

// Sulla porta interna c'è solo /v1: niente forward-auth e login delle app, niente pagine, niente
// registry e probe; /v1 risponde.
func TestInternalRouter_SoloV1(t *testing.T) {
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	cfg.IdentityServiceSecret = testSecret
	cfg.AppAuthSigningKeys = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg.AppsDomain = "apps.example.com"

	in := NewInternalRouter(cfg, discardLogger())
	pub := NewRouter(cfg, discardLogger())
	for _, path := range []string{appauth.PathVerify, appauth.PathLogin, "/healthz", "/readyz", "/", "/index.html", "/login", "/v2/"} {
		rec := httptest.NewRecorder()
		in.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("interna GET %s: %d, voluto 404", path, rec.Code)
		}
	}
	// Controprova: sulla porta pubblica le rotte di forward-auth e le probe ci sono.
	for _, path := range []string{appauth.PathKeys, "/healthz"} {
		rec := httptest.NewRecorder()
		pub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("pubblica GET %s: 404, la controprova non regge", path)
		}
	}
	rec := httptest.NewRecorder()
	in.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/meta", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("interna GET /v1/meta: %d, voluto 200", rec.Code)
	}
}

// Le chiavi pubbliche delle intestazioni H6 sono raggiungibili dalle app sulla porta interna
// (GIT-444, GITSTACK_APP_KEYS_URL), senza autenticazione; verify e login restano assenti.
func TestInternalRouter_ChiaviAppPubbliche(t *testing.T) {
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	cfg.IdentityServiceSecret = testSecret
	cfg.AppAuthSigningKeys = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg.AppsDomain = "apps.example.com"

	in := NewInternalRouter(cfg, discardLogger())
	rec := httptest.NewRecorder()
	in.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, appauth.PathKeys, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("interna GET %s: %d, voluto 200", appauth.PathKeys, rec.Code)
	}
	var body struct {
		Keys []struct {
			Kid       string `json:"kid"`
			Alg       string `json:"alg"`
			PublicKey string `json:"publicKey"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non JSON: %v", err)
	}
	if len(body.Keys) == 0 || body.Keys[0].Alg != "Ed25519" || body.Keys[0].PublicKey == "" {
		t.Errorf("chiavi inattese: %s", rec.Body.String())
	}
	// Solo GET, e verify/login restano fuori.
	for _, path := range []string{appauth.PathVerify, appauth.PathLogin} {
		r := httptest.NewRecorder()
		in.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusNotFound {
			t.Errorf("interna GET %s: %d, voluto 404", path, r.Code)
		}
	}
	// Senza chiavi di firma la rotta risponde 404, come sulla porta pubblica.
	cfg.AppAuthSigningKeys = ""
	r := httptest.NewRecorder()
	NewInternalRouter(cfg, discardLogger()).ServeHTTP(r, httptest.NewRequest(http.MethodGet, appauth.PathKeys, nil))
	if r.Code != http.StatusNotFound {
		t.Errorf("senza chiavi: %d, voluto 404", r.Code)
	}
}
