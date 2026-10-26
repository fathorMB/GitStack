package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/appauth"
	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
)

type stubProfiles struct{}

func (stubProfiles) LookupProfile(_ context.Context, id string) (identityclient.Profile, error) {
	return identityclient.Profile{ID: id, Username: "alice", Email: "alice@example.com", Teams: []string{"acme/web"}, Active: true}, nil
}

// Il login davanti alle app è montato dal router vero, fuori da /v1: Traefik lo chiama senza
// credenziali del contratto, e il rifiuto/consenso lo decide solo appauth.
func TestRouter_LoginDavantiAlleApp(t *testing.T) {
	core := newDownstream(t)
	cfg := newTestConfig(t, core.srv.URL)
	cfg.IdentityServiceSecret = testSecret
	cfg.AppAuthSigningKeys = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg.AppsDomain = "apps.example.com"
	v := &stubVerifier{results: map[string]identityclient.Result{
		"sess": {Active: true, Principal: identityclient.Caller{UserID: "u1", Username: "alice"}},
	}}
	perms := &stubPermissions{allow: func(user, resource, role string) bool { return user == "u1" && role == "read" }}
	router := NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(perms), WithProfileLookup(stubProfiles{}),
		WithClock(func() time.Time { return time.Unix(1700000000, 0) }))

	const app, repo = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	m := hmac.New(sha256.New, []byte(testSecret))
	m.Write([]byte("gitstack-appauth-v1\n" + app + "\n" + repo))
	q := url.Values{"app": {app}, "repo": {repo}, "sig": {hex.EncodeToString(m.Sum(nil))}}
	call := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", appauth.PathVerify+"?"+q.Encode(), nil)
		r.Header.Set("X-Forwarded-Host", "web-handbook-acme.apps.example.com")
		r.Header.Set("X-Forwarded-Uri", "/")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := call("gst_session=sess"); w.Code != http.StatusOK || w.Header().Get(appauth.HeaderUser) != "alice" || w.Header().Get(appauth.HeaderSignature) == "" {
		t.Fatalf("utente con read: %d %v", w.Code, w.Header())
	}
	if w := call(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("senza credenziali: %d", w.Code)
	}
	if w := call("gst_session=altra"); w.Code != http.StatusUnauthorized {
		t.Fatalf("sessione sconosciuta: %d", w.Code)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", appauth.PathKeys, nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("chiavi pubbliche: %d %v", w.Code, w.Header())
	}

	// Senza chiavi di firma il forward-auth è spento: nessuno passa.
	cfg.AppAuthSigningKeys = ""
	off := NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(perms), WithProfileLookup(stubProfiles{}))
	r := httptest.NewRequest("GET", appauth.PathVerify+"?"+q.Encode(), nil)
	r.Header.Set("X-Forwarded-Host", "web-handbook-acme.apps.example.com")
	r.Header.Set("Cookie", "gst_session=sess")
	w = httptest.NewRecorder()
	off.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("senza chiavi: %d", w.Code)
	}
}
