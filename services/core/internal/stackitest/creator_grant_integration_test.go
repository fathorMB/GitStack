//go:build integration

package stackitest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestCreatorGetsAdminGrant prova sullo stack vero (gateway + identity +
// core) che chi crea una risorsa ne diventa admin: lo stesso utente la
// legge (200) e ha il ruolo effettivo admin, un altro utente senza grant
// riceve 403.
func TestCreatorGetsAdminGrant(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")

	identityAddr, gatewayAddr := freeAddr(t), freeAddr(t)
	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret,
		httpserver.WithCreatorGranter(identityclient.New(mustURL(t, "http://"+identityAddr), serviceSecret, 5*time.Second))))
	t.Cleanup(coreSrv.Close)

	start(t, "identity", identityBin, identityAddr,
		"GITSTACK_IDENTITY_ADDR="+identityAddr,
		"GITSTACK_IDENTITY_DB_URL="+dsn,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_IDENTITY_ADMIN_USERNAME=admin",
		"GITSTACK_IDENTITY_ADMIN_PASSWORD="+adminPassword,
	)
	start(t, "gateway", gatewayBin, gatewayAddr,
		"GITSTACK_GATEWAY_ADDR="+gatewayAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	login := s.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, nil, nil)
	want(t, login, 200, "")
	var adminCookie *http.Cookie
	for _, c := range login.cookies {
		if c.Name == "gst_session" {
			adminCookie = c
		}
	}
	if adminCookie == nil {
		t.Fatalf("login admin: %s", login.body)
	}
	want(t, s.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, adminCookie), 204, "")

	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	bearer := map[string]map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		want(t, s.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, adminCookie), 201, "")
		l := s.gw("POST", "/auth/login", map[string]any{"username": name, "password": userPassword}, nil, nil)
		want(t, l, 200, "")
		var ck *http.Cookie
		for _, c := range l.cookies {
			if c.Name == "gst_session" {
				ck = c
			}
		}
		tok := s.gw("POST", "/user/tokens", map[string]any{"name": "t", "scopes": []string{"read:resource", "write:resource"}, "expiresAt": exp}, nil, ck)
		want(t, tok, 201, "")
		token, _ := tok.json()["token"].(string)
		bearer[name] = map[string]string{"Authorization": "Bearer " + token}
	}

	created := s.gw("POST", "/resources", map[string]any{"type": "repo", "name": "di-alice"}, bearer["alice"], nil)
	want(t, created, 201, "")
	id, _ := created.json()["id"].(string)
	path := "/resources/" + id

	// il creatore la legge e ne è admin
	want(t, s.gw("GET", path, nil, bearer["alice"], nil), 200, "")
	perm := s.gw("GET", path+"/permissions", nil, bearer["alice"], nil)
	want(t, perm, 200, "")
	if perm.json()["role"] != "admin" {
		t.Fatalf("ruolo effettivo del creatore = %v, voluto admin (%s)", perm.json()["role"], perm.body)
	}
	// un altro utente, senza grant, no
	want(t, s.gw("GET", path, nil, bearer["bob"], nil), 403, "forbidden")
}
