//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
)

// newOwnerEnv: identity con permessi, organizzazioni e segreto di servizio.
func newOwnerEnv(t *testing.T) *permEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	var logs bytes.Buffer
	h := httpapi.New(svc, slog.New(slog.NewTextHandler(&logs, nil)),
		httpapi.WithOrgs(orgs.New(pool, c.now)),
		httpapi.WithPermissions(permissions.New(pool, c.now)),
		httpapi.WithServiceSecret(secret))
	return &permEnv{env: &env{t: t, h: h, clock: c, users: us}, pool: pool}
}

func (e *permEnv) orgID(name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM identity.organizations WHERE name = $1`, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *permEnv) setAttrs(res uuid.UUID, ownerType string, owner string, vis string) resp {
	return e.do("PUT", "/internal/resources/"+res.String()+"/attributes",
		map[string]any{"ownerType": ownerType, "ownerId": owner, "visibility": vis}, nil, bearer(secret)...)
}

func (e *permEnv) effective(user string, res uuid.UUID, role string) (bool, any) {
	e.t.Helper()
	r := e.do("POST", "/internal/permissions/check", map[string]any{"userId": user, "resourceId": res.String(), "role": role}, nil, bearer(secret)...)
	status(e.t, r, 200)
	m := r.json()
	return m["allowed"].(bool), m["effectiveRole"]
}

func (e *permEnv) readable(user string) map[string]bool {
	e.t.Helper()
	r := e.do("POST", "/internal/permissions/readable-resources", map[string]any{"userId": user}, nil, bearer(secret)...)
	status(e.t, r, 200)
	out := map[string]bool{}
	for _, id := range r.json()["resourceIds"].([]any) {
		out[id.(string)] = true
	}
	return out
}

// P1, P6, P3 su check e readable-resources, con i casi negativi.
func TestOwnerAndVisibilityRules(t *testing.T) {
	e := newOwnerEnv(t)
	for _, n := range []string{"alice", "bob", "carol", "dave", "erin", "gone"} {
		e.mk(n, false)
	}
	e.mk("root", true)
	aliceCk := e.mustLogin("alice")
	status(t, e.do("POST", "/orgs", map[string]string{"name": "acme"}, aliceCk), 201)               // alice owner di acme
	status(t, e.do("POST", "/orgs", map[string]string{"name": "other"}, e.mustLogin("carol")), 201) // carol owner di other
	status(t, e.do("PUT", "/orgs/acme/members/bob", map[string]string{"role": "member"}, aliceCk), 200)
	acme := e.orgID("acme").String()

	repoOrg, repoOrgInt, repoPers, repoPersInt := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	status(t, e.setAttrs(repoOrg, "organization", acme, "private"), 204)
	status(t, e.setAttrs(repoOrgInt, "organization", acme, "internal"), 204)
	status(t, e.setAttrs(repoPers, "user", e.userID("dave"), "private"), 204)
	status(t, e.setAttrs(repoPersInt, "user", e.userID("dave"), "internal"), 204)
	// Idempotente, e la visibilità si può cambiare.
	status(t, e.setAttrs(repoOrg, "organization", acme, "private"), 204)

	type want struct {
		allowedRead, allowedAdmin bool
		effective                 any
	}
	cases := []struct {
		name string
		user string
		res  uuid.UUID
		want want
	}{
		// P1: owner dell'organizzazione proprietaria = admin, anche su privato.
		{"P1 owner org su repo privato", e.userID("alice"), repoOrg, want{true, true, "admin"}},
		{"P1 negativo: membro semplice, privato", e.userID("bob"), repoOrg, want{false, false, nil}},
		{"P1 negativo: owner di un'altra org", e.userID("carol"), repoOrg, want{false, false, nil}},
		// P6: proprietario del repo personale = admin.
		{"P6 proprietario", e.userID("dave"), repoPers, want{true, true, "admin"}},
		{"P6 negativo: altro utente", e.userID("erin"), repoPers, want{false, false, nil}},
		{"P6 negativo: owner di org non c'entra", e.userID("alice"), repoPers, want{false, false, nil}},
		// P3: interno = read a tutti gli utenti attivi, non write/admin.
		{"P3 interno org, utente di un'altra org", e.userID("carol"), repoOrgInt, want{true, false, "read"}},
		{"P3 interno personale, utente qualunque", e.userID("erin"), repoPersInt, want{true, false, "read"}},
		{"P3 interno: l'owner resta admin", e.userID("alice"), repoOrgInt, want{true, true, "admin"}},
		{"P3 negativo: utente sconosciuto", uuid.NewString(), repoPersInt, want{false, false, nil}},
	}
	for _, c := range cases {
		gotRead, eff := e.effective(c.user, c.res, "read")
		gotAdmin, _ := e.effective(c.user, c.res, "admin")
		if gotRead != c.want.allowedRead || gotAdmin != c.want.allowedAdmin || eff != c.want.effective {
			t.Errorf("%s: read=%v admin=%v effective=%v, atteso %+v", c.name, gotRead, gotAdmin, eff, c.want)
		}
		// Coerenza: leggibile in readable-resources se e solo se check dà read.
		if got := e.readable(c.user)[c.res.String()]; got != c.want.allowedRead {
			t.Errorf("%s: readable-resources=%v, atteso %v", c.name, got, c.want.allowedRead)
		}
	}
	// Un repo interno non è scrivibile senza grant, lo è con un grant write.
	if ok, _ := e.effective(e.userID("erin"), repoOrgInt, "write"); ok {
		t.Error("interno scrivibile senza grant")
	}
	e.sql(`INSERT INTO identity.resource_grants (id, resource_id, user_id, role) VALUES ($1, $2, $3, 'write')`, uuid.New(), repoOrgInt, e.userID("erin"))
	if ok, eff := e.effective(e.userID("erin"), repoOrgInt, "write"); !ok || eff != "write" {
		t.Errorf("interno con grant write: %v %v", ok, eff)
	}
	// Elenco: bob vede solo gli interni; alice tutti i repo di acme più gli interni personali.
	if r := e.readable(e.userID("bob")); len(r) != 2 || !r[repoOrgInt.String()] || !r[repoPersInt.String()] {
		t.Errorf("readable di bob = %v", r)
	}
	if r := e.readable(e.userID("alice")); len(r) != 3 || !r[repoOrg.String()] || r[repoPers.String()] {
		t.Errorf("readable di alice = %v", r)
	}

	// Utente disattivato: nessun ruolo, nemmeno da owner o da interno.
	e.sql(`UPDATE identity.users SET is_active = false WHERE username IN ('alice', 'dave', 'gone')`)
	for _, c := range []struct {
		user string
		res  uuid.UUID
	}{{e.userID("alice"), repoOrg}, {e.userID("dave"), repoPers}, {e.userID("gone"), repoPersInt}} {
		if ok, eff := e.effective(c.user, c.res, "read"); ok || eff != nil {
			t.Errorf("utente disattivato su %s: %v %v", c.res, ok, eff)
		}
		if r := e.readable(c.user); len(r) != 0 {
			t.Errorf("utente disattivato: readable = %v", r)
		}
	}

	// Owner: non cambia (409), deve esistere (404), valori validi (400).
	status(t, e.setAttrs(repoOrg, "organization", e.orgID("other").String(), "private"), 409)
	status(t, e.setAttrs(repoOrg, "user", e.userID("erin"), "private"), 409)
	status(t, e.setAttrs(uuid.New(), "user", uuid.NewString(), "private"), 404)
	status(t, e.setAttrs(uuid.New(), "team", acme, "private"), 400)
	status(t, e.setAttrs(uuid.New(), "user", e.userID("erin"), "public"), 400)
	errCode(t, e.do("PUT", "/internal/resources/"+uuid.NewString()+"/attributes",
		map[string]any{"ownerType": "user", "ownerId": e.userID("erin"), "visibility": "private"}, nil), 401, "unauthenticated")

	// resolveOwner: lo spazio di nomi unico.
	r := e.do("GET", "/internal/owners/acme", nil, nil, bearer(secret)...)
	status(t, r, 200)
	if m := r.json(); m["type"] != "organization" || m["id"] != acme || m["name"] != "acme" {
		t.Errorf("resolveOwner acme = %s", r.body)
	}
	r = e.do("GET", "/internal/owners/erin", nil, nil, bearer(secret)...)
	status(t, r, 200)
	if m := r.json(); m["type"] != "user" || m["id"] != e.userID("erin") {
		t.Errorf("resolveOwner erin = %s", r.body)
	}
	errCode(t, e.do("GET", "/internal/owners/nessuno", nil, nil, bearer(secret)...), 404, "not_found")
}

// R1: spazio di nomi unico e nomi riservati.
func TestSharedNamespaceAndReservedNames(t *testing.T) {
	e := newOwnerEnv(t)
	e.mk("root", true)
	e.mk("alice", false)
	root := e.mustLogin("root")
	aliceCk := e.mustLogin("alice")

	// Organizzazione con il nome di un utente: 409.
	errCode(t, e.do("POST", "/orgs", map[string]string{"name": "alice"}, aliceCk), 409, "already_exists")
	status(t, e.do("POST", "/orgs", map[string]string{"name": "acme"}, aliceCk), 201)
	// Utente con il nome di un'organizzazione: 409.
	r := e.do("POST", "/users", map[string]any{"username": "acme", "email": "acme@example.com", "password": pw}, root)
	errCode(t, r, 409, "already_exists")
	// Un nome libero resta creabile (e poi occupato anche per le org).
	status(t, e.do("POST", "/users", map[string]any{"username": "carol", "email": "carol@example.com", "password": pw}, root), 201)
	errCode(t, e.do("POST", "/orgs", map[string]string{"name": "carol"}, aliceCk), 409, "already_exists")

	// Nomi riservati: 400, per utenti e organizzazioni.
	for _, n := range []string{"login", "settings", "api", "admin"} {
		errCode(t, e.do("POST", "/orgs", map[string]string{"name": n}, aliceCk), 400, "reserved_name")
		errCode(t, e.do("POST", "/users", map[string]any{"username": n, "email": n + "@example.com", "password": pw}, root), 400, "reserved_name")
	}
	// Eliminare un utente libera il nome per un'organizzazione.
	status(t, e.do("DELETE", "/users/carol", nil, root), 204)
	status(t, e.do("POST", "/orgs", map[string]string{"name": "carol"}, aliceCk), 201)
}
