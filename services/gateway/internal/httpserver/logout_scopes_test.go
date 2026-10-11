package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
)

// Dopo POST /v1/auth/logout la stessa sessione riceve subito 401, senza
// aspettare il TTL della cache (qui 10 minuti, con orologio fermo).
func TestAuth_LogoutSvuotaLaCache(t *testing.T) {
	var revoked atomic.Bool
	var verifyCalls atomic.Int32
	fakeIdentity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/verify" {
			if r.URL.Path == "/auth/logout" {
				revoked.Store(true)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		verifyCalls.Add(1)
		var in struct{ Credential string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Credential != "sess" || revoked.Load() {
			_, _ = w.Write([]byte(`{"active":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"33333333-3333-3333-3333-333333333333","username":"admin","kind":"human","isAdmin":true,"authMethod":"password","mustChangePassword":false}}`))
	}))
	defer fakeIdentity.Close()
	core := newDownstream(t)

	cfg := newTestConfig(t, core.srv.URL)
	cfg.IdentityURL = mustURL(t, fakeIdentity.URL)
	cfg.IdentityTimeout = 5 * time.Second
	cfg.IdentityServiceSecret = testSecret
	cfg.AuthCacheTTL = 10 * time.Minute
	cfg.AuthCacheNegativeTTL = 10 * time.Minute
	router := NewRouter(cfg, discardLogger(), WithClock(func() time.Time { return time.Unix(1700000000, 0) }))
	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, cookieReq(method, path, "sess"))
		return rec.Code
	}

	if got := do("GET", "/v1/resources"); got != http.StatusNoContent {
		t.Fatalf("prima del logout: %d", got)
	}
	do("GET", "/v1/resources")
	if verifyCalls.Load() != 1 {
		t.Fatalf("la cache non funziona: %d verifiche", verifyCalls.Load())
	}
	if got := do("POST", "/v1/auth/logout"); got != http.StatusNoContent {
		t.Fatalf("logout: %d", got)
	}
	if got := do("GET", "/v1/resources"); got != http.StatusUnauthorized {
		t.Fatalf("subito dopo il logout: %d, voluto 401 senza aspettare il TTL", got)
	}
}

// Un token con solo read:user non scrive né legge le risorse; con
// read:resource legge ma non scrive.
func TestAuth_ScopeSulleOperazioniResources(t *testing.T) {
	v := &stubVerifier{results: map[string]identityclient.Result{
		"gst_u":  tokenResult("read:user"),
		"gst_r":  tokenResult("read:resource"),
		"gst_rw": tokenResult("read:resource", "write:resource"),
	}}
	cases := []struct {
		token, method, path string
		want                int
	}{
		{"gst_u", "POST", "/v1/resources", 403},
		{"gst_u", "PATCH", "/v1/resources/00000000-0000-0000-0000-000000000001", 403},
		{"gst_u", "DELETE", "/v1/resources/00000000-0000-0000-0000-000000000001", 403},
		{"gst_u", "GET", "/v1/resources", 403},
		{"gst_u", "GET", "/v1/resources/00000000-0000-0000-0000-000000000001", 403},
		{"gst_r", "GET", "/v1/resources", 204},
		{"gst_r", "GET", "/v1/resources/00000000-0000-0000-0000-000000000001", 204},
		{"gst_r", "POST", "/v1/resources", 403},
		{"gst_r", "DELETE", "/v1/resources/00000000-0000-0000-0000-000000000001", 403},
		{"gst_rw", "POST", "/v1/resources", 204},
		{"gst_rw", "PATCH", "/v1/resources/00000000-0000-0000-0000-000000000001", 204},
		{"gst_rw", "DELETE", "/v1/resources/00000000-0000-0000-0000-000000000001", 204},
	}
	for _, c := range cases {
		e := newEnv(t, v)
		rec := e.do(bearerReq(c.method, c.path, c.token))
		if rec.Code != c.want {
			t.Errorf("%s %s con %s: %d, voluto %d (%s)", c.method, c.path, c.token, rec.Code, c.want, rec.Body.String())
			continue
		}
		if c.want == 403 && errorCode(t, rec) != "insufficient_scope" {
			t.Errorf("%s %s: code %s", c.method, c.path, rec.Body.String())
		}
	}
}
