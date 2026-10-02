//go:build integration

package httpapi_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// newOrgEnv come newEnv, ma con il servizio delle organizzazioni attivo.
func newOrgEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	h := httpapi.New(svc, nil, httpapi.WithOrgs(orgs.New(pool, c.now)))
	return &env{t: t, h: h, clock: c, users: us}
}

// orgFixture: root (admin di sistema, non membro), alice (owner di acme),
// bob (member di acme), carol (fuori dall'organizzazione).
type orgFixture struct {
	e                       *env
	root, alice, bob, carol *http.Cookie
}

func newOrgFixture(t *testing.T) orgFixture {
	t.Helper()
	e := newOrgEnv(t)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	e.mk("carol", false)
	f := orgFixture{e: e, root: e.mustLogin("root"), alice: e.mustLogin("alice"), bob: e.mustLogin("bob"), carol: e.mustLogin("carol")}
	status(t, e.do("POST", "/orgs", map[string]any{"name": "acme", "displayName": "Acme Inc", "description": "d"}, f.alice), 201)
	status(t, e.do("PUT", "/orgs/acme/members/bob", map[string]string{"role": "member"}, f.alice), 200)
	return f
}

func memberRoles(t *testing.T, e *env, ck *http.Cookie) map[string]string {
	t.Helper()
	r := e.do("GET", "/orgs/acme/members", nil, ck)
	status(t, r, 200)
	out := map[string]string{}
	for _, it := range r.json()["items"].([]any) {
		m := it.(map[string]any)
		out[m["user"].(map[string]any)["username"].(string)] = m["role"].(string)
	}
	return out
}

// Regola 1: nome univoco, slug non valido → 422 con details.fields; campi completi nelle risposte.
func TestOrgCreateUniqueAndValidation(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	r := e.do("POST", "/orgs", map[string]any{"name": "proj3", "displayName": "Proj Tre", "description": "desc"}, f.alice)
	status(t, r, 201)
	if m := r.json(); m["displayName"] != "Proj Tre" || m["description"] != "desc" || m["name"] != "proj3" {
		t.Fatalf("risposta di creazione incompleta: %s", r.body)
	}
	errCode(t, e.do("POST", "/orgs", map[string]string{"name": "proj3"}, f.bob), 409, "already_exists")
	for _, bad := range []string{"Bad_Name", "-start", "ab--"} {
		r = e.do("POST", "/orgs", map[string]string{"name": bad}, f.alice)
		errCode(t, r, 422, "validation_failed")
		if r.json()["error"].(map[string]any)["details"].(map[string]any)["fields"] == nil {
			t.Fatalf("details.fields mancante: %s", r.body)
		}
	}
	// Lista: displayName e description presenti.
	r = e.do("GET", "/orgs", nil, f.alice)
	status(t, r, 200)
	found := false
	for _, it := range r.json()["items"].([]any) {
		if m := it.(map[string]any); m["name"] == "acme" {
			found = m["displayName"] == "Acme Inc" && m["description"] == "d"
		}
	}
	if !found {
		t.Fatalf("lista senza displayName/description: %s", r.body)
	}
	errCode(t, e.do("POST", "/orgs", map[string]string{"name": "x1"}, nil), 401, "unauthenticated")
}

// Regola 2: chi crea l'organizzazione ne diventa owner.
func TestOrgCreatorBecomesOwner(t *testing.T) {
	f := newOrgFixture(t)
	roles := memberRoles(t, f.e, f.alice)
	if roles["alice"] != "owner" || roles["bob"] != "member" || len(roles) != 2 {
		t.Fatalf("membri inattesi: %v", roles)
	}
}

// Regola 4 (a): chi non e' owner riceve 403 su ogni modifica.
func TestOrgNonOwnerForbidden(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	status(t, e.do("POST", "/orgs/acme/teams", map[string]string{"name": "dev"}, f.alice), 201)
	for name, ck := range map[string]*http.Cookie{"member": f.bob, "esterno": f.carol} {
		for _, c := range []struct {
			m, p string
			body any
		}{
			{"PATCH", "/orgs/acme", map[string]string{"description": "x"}},
			{"DELETE", "/orgs/acme", nil},
			{"PUT", "/orgs/acme/members/carol", map[string]string{"role": "member"}},
			{"DELETE", "/orgs/acme/members/alice", nil},
			{"POST", "/orgs/acme/teams", map[string]string{"name": "ops"}},
			{"PATCH", "/orgs/acme/teams/dev", map[string]string{"description": "x"}},
			{"DELETE", "/orgs/acme/teams/dev", nil},
			{"PUT", "/orgs/acme/teams/dev/members/bob", map[string]string{"role": "member"}},
			{"DELETE", "/orgs/acme/teams/dev/members/bob", nil},
		} {
			r := e.do(c.m, c.p, c.body, ck)
			if r.StatusCode != 403 {
				t.Fatalf("%s %s %s: %d atteso 403: %s", name, c.m, c.p, r.StatusCode, r.body)
			}
			errCode(t, r, 403, "forbidden")
		}
	}
	// Letture: un esterno riceve 403, un member passa.
	for _, p := range []string{"/orgs/acme", "/orgs/acme/members", "/orgs/acme/teams", "/orgs/acme/teams/dev", "/orgs/acme/teams/dev/members"} {
		errCode(t, e.do("GET", p, nil, f.carol), 403, "forbidden")
		status(t, e.do("GET", p, nil, f.bob), 200)
	}
	// Eccezione: un member puo' rimuovere se stesso.
	status(t, e.do("DELETE", "/orgs/acme/members/bob", nil, f.bob), 204)
}

// Regola 4 (b): un admin di sistema non membro puo' sempre.
func TestOrgSystemAdminNotMember(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	status(t, e.do("GET", "/orgs/acme", nil, f.root), 200)
	status(t, e.do("PUT", "/orgs/acme/members/carol", map[string]string{"role": "member"}, f.root), 200)
	r := e.do("POST", "/orgs/acme/teams", map[string]any{"name": "dev", "description": "squadra"}, f.root)
	status(t, r, 201)
	if r.json()["description"] != "squadra" {
		t.Fatalf("team senza description: %s", r.body)
	}
	status(t, e.do("PATCH", "/orgs/acme/teams/dev", map[string]string{"description": "nuova"}, f.root), 200)
	status(t, e.do("PUT", "/orgs/acme/teams/dev/members/carol", map[string]string{"role": "maintainer"}, f.root), 200)
	status(t, e.do("DELETE", "/orgs/acme/teams/dev/members/carol", nil, f.root), 204)
	status(t, e.do("DELETE", "/orgs/acme/teams/dev", nil, f.root), 204)
	status(t, e.do("PATCH", "/orgs/acme", map[string]string{"description": "nuova"}, f.root), 200)
	status(t, e.do("DELETE", "/orgs/acme/members/carol", nil, f.root), 204)
	status(t, e.do("DELETE", "/orgs/acme", nil, f.root), 204)
}

// Regola 3 (c): l'ultimo owner non si declassa ne' si rimuove.
func TestOrgLastOwner(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	errCode(t, e.do("PUT", "/orgs/acme/members/alice", map[string]string{"role": "member"}, f.alice), 409, "last_owner")
	errCode(t, e.do("DELETE", "/orgs/acme/members/alice", nil, f.alice), 409, "last_owner")
	errCode(t, e.do("DELETE", "/orgs/acme/members/alice", nil, f.root), 409, "last_owner")
	if roles := memberRoles(t, e, f.alice); roles["alice"] != "owner" {
		t.Fatalf("alice non e' piu' owner: %v", roles)
	}
	// Con un secondo owner il declassamento e la rimozione riescono.
	status(t, e.do("PUT", "/orgs/acme/members/bob", map[string]string{"role": "owner"}, f.alice), 200)
	status(t, e.do("PUT", "/orgs/acme/members/alice", map[string]string{"role": "member"}, f.bob), 200)
	errCode(t, e.do("DELETE", "/orgs/acme/members/bob", nil, f.bob), 409, "last_owner")
	// Declassare un member a member non e' un problema.
	status(t, e.do("PUT", "/orgs/acme/members/alice", map[string]string{"role": "member"}, f.bob), 200)
}

// Regola 3 (d): due owner che si declassano a vicenda: uno passa, l'altro 409.
func TestOrgLastOwnerConcurrent(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	status(t, e.do("PUT", "/orgs/acme/members/bob", map[string]string{"role": "owner"}, f.alice), 200)
	// Login gia' fatti: nessuna richiesta di login dentro le goroutine.
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, 2)
	bodies := make([]string, 2)
	run := func(i int, ck *http.Cookie, target string) {
		defer wg.Done()
		<-start
		r := e.do("PUT", "/orgs/acme/members/"+target, map[string]string{"role": "member"}, ck)
		codes[i], bodies[i] = r.StatusCode, string(r.body)
	}
	wg.Add(2)
	go run(0, f.alice, "alice")
	go run(1, f.bob, "bob")
	close(start)
	wg.Wait()
	ok, conflict := 0, 0
	for i, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
			if want := `"last_owner"`; !contains(bodies[i], want) {
				t.Fatalf("409 senza last_owner: %s", bodies[i])
			}
		default:
			t.Fatalf("status inatteso %d: %s", c, bodies[i])
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("attesi un 200 e un 409, ottenuti %v", codes)
	}
	owners := 0
	for _, role := range memberRoles(t, e, f.root) {
		if role == "owner" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owner rimasti %d, atteso 1", owners)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// (e) utente bersaglio inesistente → 404.
func TestOrgUnknownTargetUser(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	status(t, e.do("POST", "/orgs/acme/teams", map[string]string{"name": "dev"}, f.alice), 201)
	errCode(t, e.do("PUT", "/orgs/acme/members/nessuno", map[string]string{"role": "member"}, f.alice), 404, "not_found")
	errCode(t, e.do("DELETE", "/orgs/acme/members/nessuno", nil, f.alice), 404, "not_found")
	errCode(t, e.do("PUT", "/orgs/acme/teams/dev/members/nessuno", map[string]string{"role": "member"}, f.alice), 404, "not_found")
	errCode(t, e.do("DELETE", "/orgs/acme/teams/dev/members/nessuno", nil, f.alice), 404, "not_found")
	errCode(t, e.do("GET", "/orgs/inesistente", nil, f.alice), 404, "not_found")
}

// Regola 5: team di una sola org; solo membri dell'org; body senza role → member.
func TestTeamMembers(t *testing.T) {
	f := newOrgFixture(t)
	e := f.e
	r := e.do("POST", "/orgs/acme/teams", map[string]any{"name": "dev", "description": "squadra"}, f.alice)
	status(t, r, 201)
	if m := r.json(); m["description"] != "squadra" || m["orgId"] == nil {
		t.Fatalf("team incompleto: %s", r.body)
	}
	errCode(t, e.do("POST", "/orgs/acme/teams", map[string]string{"name": "dev"}, f.alice), 409, "already_exists")
	// carol non e' membro dell'organizzazione.
	errCode(t, e.do("PUT", "/orgs/acme/teams/dev/members/carol", map[string]string{"role": "member"}, f.alice), 422, "not_org_member")
	// Body vuoto: ruolo di default member, nessun panic.
	r = e.do("PUT", "/orgs/acme/teams/dev/members/bob", map[string]string{}, f.alice)
	status(t, r, 200)
	if r.json()["role"] != "member" {
		t.Fatalf("ruolo di default errato: %s", r.body)
	}
	status(t, e.do("PUT", "/orgs/acme/teams/dev/members/bob", map[string]string{"role": "maintainer"}, f.alice), 200)
	r = e.do("GET", "/orgs/acme/teams/dev/members", nil, f.bob)
	status(t, r, 200)
	if items := r.json()["items"].([]any); len(items) != 1 || items[0].(map[string]any)["role"] != "maintainer" {
		t.Fatalf("membri team inattesi: %s", r.body)
	}
	// Lo stesso nome di team in un'altra org e' un altro team.
	status(t, e.do("POST", "/orgs", map[string]string{"name": "other"}, f.carol), 201)
	status(t, e.do("POST", "/orgs/other/teams", map[string]string{"name": "dev"}, f.carol), 201)
	// Team inesistente → 404.
	errCode(t, e.do("GET", "/orgs/acme/teams/zzz", nil, f.alice), 404, "not_found")
	status(t, e.do("DELETE", "/orgs/acme/teams/dev/members/bob", nil, f.alice), 204)
}
