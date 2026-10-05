//go:build integration

package stackitest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
)

// TestListResourcesFilteredByReadPermission prova sullo stack vero che
// GET /resources mostra a ciascuno solo le risorse con almeno read (grant
// diretto, via team, via owner dell'organizzazione), che una risorsa
// concessa a un'altra organizzazione non compare, che l'admin di sistema le
// vede tutte, che total e paginazione contano solo le visibili e che, se
// identity non risponde, core risponde 503 senza nessun elenco.
func TestListResourcesFilteredByReadPermission(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")

	identityAddr, gatewayAddr := freeAddr(t), freeAddr(t)

	// Fra core e identity c'è un proxy che si può "spegnere": il gateway
	// continua a parlare con identity, solo core la perde.
	var down atomic.Bool
	proxy := httputil.NewSingleHostReverseProxy(mustURL(t, "http://"+identityAddr))
	identityProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			http.Error(w, "giù", http.StatusBadGateway)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(identityProxy.Close)

	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret,
		httpserver.WithCreatorGranter(identityclient.New(mustURL(t, identityProxy.URL), serviceSecret, 5*time.Second))))
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
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	// --- admin di sistema -----------------------------------------------------
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

	// --- cinque risorse -------------------------------------------------------
	resID := map[string]string{}
	for _, n := range []string{"r1", "r2", "r3", "r4", "r5"} {
		c := s.gw("POST", "/resources", map[string]any{"type": "repo", "name": "lst-" + n}, nil, adminCookie)
		want(t, c, 201, "")
		resID[n], _ = c.json()["id"].(string)
	}

	// --- utenti ---------------------------------------------------------------
	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	userID := map[string]string{}
	bearer := map[string]map[string]string{}
	for _, name := range []string{"alice", "bob", "carol", "dave", "erin", "frank"} {
		u := s.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, adminCookie)
		want(t, u, 201, "")
		userID[name], _ = u.json()["id"].(string)
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

	// --- due organizzazioni con un team ciascuna (via SQL, come in permissions) -
	acme, web, other, ops := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity.organizations (id, name) VALUES ($1, 'acme')`, []any{acme}},
		{`INSERT INTO identity.organizations (id, name) VALUES ($1, 'altra')`, []any{other}},
		{`INSERT INTO identity.teams (id, org_id, name) VALUES ($1, $2, 'web')`, []any{web, acme}},
		{`INSERT INTO identity.teams (id, org_id, name) VALUES ($1, $2, 'ops')`, []any{ops, other}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{acme, userID["carol"]}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{other, userID["erin"]}},
		{`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, []any{acme, userID["bob"]}},
		{`INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, []any{web, acme, userID["bob"]}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	// --- grant read ------------------------------------------------------------
	grant := func(res, subjectType, subjectID string) {
		t.Helper()
		want(t, s.gw("POST", "/resources/"+resID[res]+"/grants",
			map[string]any{"subjectType": subjectType, "subjectId": subjectID, "role": "read"}, nil, adminCookie), 201, "")
	}
	grant("r1", "user", userID["alice"])
	grant("r2", "team", web.String())    // bob (membro) e carol (owner di acme)
	grant("r3", "team", ops.String())    // team dell'altra organizzazione: erin sì, carol no
	grant("r1", "user", userID["frank"]) // frank: tre grant diretti
	grant("r2", "user", userID["frank"])
	grant("r3", "user", userID["frank"])

	type listing struct {
		status  int
		body    string
		ids     []string
		total   int
		page    int
		perPage int
	}
	list := func(h map[string]string, ck *http.Cookie, query string) listing {
		t.Helper()
		r := s.gw("GET", "/resources"+query, nil, h, ck)
		out := listing{status: r.status, body: string(r.body)}
		if r.status != 200 {
			return out
		}
		j := r.json()
		items, _ := j["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			id, _ := m["id"].(string)
			out.ids = append(out.ids, id)
		}
		if items == nil {
			t.Fatalf("items assente o null: %s", r.body)
		}
		total, _ := j["total"].(float64)
		page, _ := j["page"].(float64)
		perPage, _ := j["perPage"].(float64)
		out.total, out.page, out.perPage = int(total), int(page), int(perPage)
		return out
	}
	sameSet := func(t *testing.T, who string, got listing, wantNames ...string) {
		t.Helper()
		if got.status != 200 {
			t.Fatalf("%s: GET /resources = %d (%s)", who, got.status, got.body)
		}
		wantIDs := map[string]bool{}
		for _, n := range wantNames {
			wantIDs[resID[n]] = true
		}
		if len(got.ids) != len(wantIDs) || got.total != len(wantIDs) {
			t.Errorf("%s: vede %d risorse (total %d), volute %v: %s", who, len(got.ids), got.total, wantNames, got.body)
		}
		for _, id := range got.ids {
			if !wantIDs[id] {
				t.Errorf("%s: vede la risorsa %s che non dovrebbe vedere", who, id)
			}
		}
	}

	t.Run("senza grant: elenco vuoto e nessun dato altrui", func(t *testing.T) {
		got := list(bearer["dave"], nil, "")
		sameSet(t, "dave", got)
		if got.total != 0 {
			t.Errorf("total = %d, voluto 0", got.total)
		}
		for _, n := range []string{"r1", "r2", "r3", "r4", "r5"} {
			if strings.Contains(got.body, resID[n]) || strings.Contains(got.body, "lst-"+n) {
				t.Errorf("il corpo rivela la risorsa %s: %s", n, got.body)
			}
		}
	})

	t.Run("grant diretto, via team, via owner dell'organizzazione", func(t *testing.T) {
		sameSet(t, "alice (diretto)", list(bearer["alice"], nil, ""), "r1")
		sameSet(t, "bob (via team)", list(bearer["bob"], nil, ""), "r2")
		sameSet(t, "carol (owner di acme)", list(bearer["carol"], nil, ""), "r2")
	})

	t.Run("risorsa di un'altra organizzazione", func(t *testing.T) {
		// r3 è concessa al team ops dell'organizzazione «altra»: carol (owner
		// di acme) e bob non la vedono, erin (owner di «altra») sì e non
		// vede r2.
		for _, who := range []string{"carol", "bob"} {
			for _, id := range list(bearer[who], nil, "").ids {
				if id == resID["r3"] {
					t.Errorf("%s vede la risorsa dell'altra organizzazione", who)
				}
			}
		}
		sameSet(t, "erin (owner di altra)", list(bearer["erin"], nil, ""), "r3")
	})

	t.Run("admin di sistema vede tutto, con più pagine", func(t *testing.T) {
		all := list(nil, adminCookie, "")
		sameSet(t, "admin", all, "r1", "r2", "r3", "r4", "r5")
		want5 := []int{2, 2, 1}
		for i, n := range want5 {
			p := list(nil, adminCookie, "?page="+string(rune('1'+i))+"&perPage=2")
			if p.status != 200 || len(p.ids) != n || p.total != 5 || p.page != i+1 || p.perPage != 2 {
				t.Errorf("admin pagina %d: %+v", i+1, p)
			}
		}
	})

	t.Run("total e pagine contano solo le visibili", func(t *testing.T) {
		// frank legge 3 risorse su 5, con perPage=2: total 3, pagina 2 con 1.
		p1 := list(bearer["frank"], nil, "?page=1&perPage=2")
		p2 := list(bearer["frank"], nil, "?page=2&perPage=2")
		p3 := list(bearer["frank"], nil, "?page=3&perPage=2")
		if p1.status != 200 || len(p1.ids) != 2 || p1.total != 3 || p1.page != 1 || p1.perPage != 2 {
			t.Errorf("pagina 1: %+v", p1)
		}
		if p2.status != 200 || len(p2.ids) != 1 || p2.total != 3 || p2.page != 2 {
			t.Errorf("pagina 2: %+v", p2)
		}
		if p3.status != 200 || len(p3.ids) != 0 || p3.total != 3 {
			t.Errorf("pagina 3: %+v", p3)
		}
		seen := map[string]bool{}
		for _, id := range append(p1.ids, p2.ids...) {
			if seen[id] {
				t.Errorf("risorsa %s ripetuta fra le pagine", id)
			}
			seen[id] = true
		}
		for _, n := range []string{"r1", "r2", "r3"} {
			if !seen[resID[n]] {
				t.Errorf("manca %s fra le pagine di frank", n)
			}
		}
		// il filtro per tipo si somma ai permessi
		sameSet(t, "frank type=repo", list(bearer["frank"], nil, "?type=repo"), "r1", "r2", "r3")
		sameSet(t, "frank type=altro", list(bearer["frank"], nil, "?type=altro"))
	})

	t.Run("identity non risponde: 503 e nessun elenco", func(t *testing.T) {
		down.Store(true)
		t.Cleanup(func() { down.Store(false) })
		for name, c := range map[string]struct {
			h  map[string]string
			ck *http.Cookie
		}{"utente": {bearer["alice"], nil}, "admin": {nil, adminCookie}} {
			r := s.gw("GET", "/resources", nil, c.h, c.ck)
			want(t, r, 503, "identity_unavailable")
			if strings.Contains(string(r.body), "items") || strings.Contains(string(r.body), "lst-") {
				t.Errorf("%s: la 503 contiene un elenco: %s", name, r.body)
			}
		}
	})
}
