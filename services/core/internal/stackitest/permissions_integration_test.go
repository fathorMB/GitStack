//go:build integration

package stackitest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/google/uuid"
)

// TestPermissionsGatewayIdentityCore prova il modello dei permessi sullo
// stack vero: grant diretto, grant via team, ereditarietà dall'owner
// dell'organizzazione, utente senza grant (403), 403 uguale per una risorsa
// inesistente, effetto immediato di modifica e revoca dei grant.
func TestPermissionsGatewayIdentityCore(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")

	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret))
	t.Cleanup(coreSrv.Close)

	identityAddr, gatewayAddr := freeAddr(t), freeAddr(t)
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
		// Niente cache utile: i permessi non si tengono comunque in cache.
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	// --- admin: cambia la password iniziale e crea la risorsa -----------------
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
	created := s.gw("POST", "/resources", map[string]any{"type": "repo", "name": "permessi"}, nil, adminCookie)
	want(t, created, 201, "")
	res, _ := created.json()["id"].(string)
	resPath := "/resources/" + res

	// --- utenti, ciascuno con un token read:resource + write:resource ---------
	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	ids := map[string]string{}
	bearer := map[string]map[string]string{}
	for _, name := range []string{"alice", "bob", "carol", "dave", "erin"} {
		u := s.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, adminCookie)
		want(t, u, 201, "")
		ids[name], _ = u.json()["id"].(string)
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

	// --- organizzazione, team, membership (via SQL: GIT-37 è un altro item) ---
	acme, web := uuid.New(), uuid.New()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity.organizations (id, name) VALUES ($1, 'acme')`, []any{acme}},
		{`INSERT INTO identity.teams (id, org_id, name) VALUES ($1, $2, 'web')`, []any{web, acme}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{acme, ids["carol"]}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, []any{acme, ids["bob"]}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, []any{acme, ids["erin"]}},
		{`INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, []any{web, acme, ids["bob"]}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	// --- grant, dalla API del gateway -----------------------------------------
	aliceGrant := s.gw("POST", resPath+"/grants", map[string]any{"subjectType": "user", "subjectId": ids["alice"], "role": "write"}, nil, adminCookie)
	want(t, aliceGrant, 201, "")
	teamGrant := s.gw("POST", resPath+"/grants", map[string]any{"subjectType": "team", "subjectId": web.String(), "role": "read"}, nil, adminCookie)
	want(t, teamGrant, 201, "")
	aliceGrantID, _ := aliceGrant.json()["id"].(string)
	teamGrantID, _ := teamGrant.json()["id"].(string)
	// Il secondo grant per lo stesso soggetto: 409.
	want(t, s.gw("POST", resPath+"/grants", map[string]any{"subjectType": "user", "subjectId": ids["alice"], "role": "admin"}, nil, adminCookie), 409, "")

	patch := map[string]any{"name": "rinominata"}
	// check esegue GET/PATCH/DELETE e confronta gli esiti; il DELETE è provato
	// solo quando serve, perché elimina la risorsa.
	check := func(who string, get, patchWant int) {
		t.Helper()
		h := bearer[who]
		if r := s.gw("GET", resPath, nil, h, nil); r.status != get {
			t.Errorf("%s GET: %d (%s), voluto %d", who, r.status, r.body, get)
		} else if get == 403 && r.errCode() != "forbidden" {
			t.Errorf("%s GET: code %q", who, r.errCode())
		}
		if r := s.gw("PATCH", resPath, patch, h, nil); r.status != patchWant {
			t.Errorf("%s PATCH: %d (%s), voluto %d", who, r.status, r.body, patchWant)
		}
	}

	// grant diretto write: legge e scrive
	check("alice", 200, 200)
	// via team (read): legge, non scrive
	check("bob", 200, 403)
	// owner dell'organizzazione: eredita il ruolo del team (read)
	check("carol", 200, 403)
	// membro dell'org ma non del team, e utente senza grant: nessun accesso
	check("erin", 403, 403)
	check("dave", 403, 403)

	// Nessuna credenziale: 401, non 403.
	want(t, s.gw("GET", resPath, nil, nil, nil), 401, "unauthenticated")

	// 403 identico per una risorsa che non esiste: nessun 404 che la riveli.
	ghost := "/resources/" + uuid.NewString()
	real403 := s.gw("GET", resPath, nil, bearer["dave"], nil)
	ghost403 := s.gw("GET", ghost, nil, bearer["dave"], nil)
	want(t, ghost403, 403, "forbidden")
	if string(real403.body) != string(ghost403.body) || strings.Contains(string(ghost403.body), ghost[len("/resources/"):]) {
		t.Errorf("403 distinguibile: %s / %s", real403.body, ghost403.body)
	}
	// L'admin di sistema passa e vede il 404 vero di core.
	want(t, s.gw("GET", ghost, nil, nil, adminCookie), 404, "")

	// Il grant del team sale a write: bob e carol scrivono subito.
	want(t, s.gw("PATCH", resPath+"/grants/"+teamGrantID, map[string]any{"role": "write"}, nil, adminCookie), 200, "")
	check("bob", 200, 200)
	check("carol", 200, 200)
	check("dave", 403, 403)

	// Chi ha solo write non gestisce i grant (serve admin sulla risorsa).
	want(t, s.gw("GET", resPath+"/grants", nil, bearer["bob"], nil), 403, "forbidden")
	// Elimina: serve admin. Alice (write) no; con admin sì.
	want(t, s.gw("DELETE", resPath, nil, bearer["alice"], nil), 403, "forbidden")
	want(t, s.gw("PATCH", resPath+"/grants/"+aliceGrantID, map[string]any{"role": "admin"}, nil, adminCookie), 200, "")
	// Con admin alice gestisce i grant...
	want(t, s.gw("GET", resPath+"/grants", nil, bearer["alice"], nil), 200, "")
	// ...e vede il proprio permesso effettivo.
	eff := s.gw("GET", resPath+"/permissions", nil, bearer["alice"], nil)
	want(t, eff, 200, "")
	if eff.json()["role"] != "admin" {
		t.Errorf("permesso effettivo di alice: %s", eff.body)
	}

	// Revoca del grant del team: bob e carol perdono l'accesso all'istante.
	want(t, s.gw("DELETE", resPath+"/grants/"+teamGrantID, nil, bearer["alice"], nil), 204, "")
	check("bob", 403, 403)
	check("carol", 403, 403)

	// Alice (admin) elimina la risorsa; subito dopo, per un utente senza grant
	// la risposta è sempre 403.
	want(t, s.gw("DELETE", resPath, nil, bearer["alice"], nil), 204, "")
	want(t, s.gw("GET", resPath, nil, bearer["dave"], nil), 403, "forbidden")
	want(t, s.gw("GET", resPath, nil, nil, adminCookie), 404, "")

	// core direttamente: senza identità firmata dal gateway resta 401.
	want(t, s.do("GET", s.core+resPath, nil, nil, nil), 401, "unauthenticated")
}
