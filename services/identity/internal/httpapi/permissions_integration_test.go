//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type permEnv struct {
	*env
	pool *pgxpool.Pool
}

func newPermEnv(t *testing.T) *permEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	var logs bytes.Buffer
	h := httpapi.New(svc, slog.New(slog.NewTextHandler(&logs, nil)),
		httpapi.WithTokens(apitokens.New(pool, c.now, 30*24*time.Hour)),
		httpapi.WithPermissions(permissions.New(pool, c.now)),
		httpapi.WithServiceSecret(secret))
	return &permEnv{env: &env{t: t, h: h, clock: c, users: us}, pool: pool}
}

func (e *permEnv) userID(name string) string {
	e.t.Helper()
	u, err := e.users.Get(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID.String()
}

func (e *permEnv) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func TestPermissionsOverHTTP(t *testing.T) {
	e := newPermEnv(t)
	e.mk("root", true)
	e.mk("alice", false) // admin della risorsa (grant diretto)
	e.mk("bob", false)   // via team
	e.mk("carol", false) // senza grant
	rootCk, aliceCk, carolCk := e.mustLogin("root"), e.mustLogin("alice"), e.mustLogin("carol")
	res := uuid.NewString()
	base := "/resources/" + res

	// Senza sessione: 401.
	errCode(t, e.do("GET", base+"/permissions", nil, nil), 401, "unauthenticated")
	errCode(t, e.do("GET", base+"/grants", nil, nil), 401, "unauthenticated")

	// Senza il ruolo admin: 403, anche per elencare; il permesso effettivo
	// invece risponde 200 con role null.
	errCode(t, e.do("GET", base+"/grants", nil, carolCk), 403, "forbidden")
	errCode(t, e.do("POST", base+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID("carol"), "role": "admin"}, carolCk), 403, "forbidden")
	p := e.do("GET", base+"/permissions", nil, carolCk)
	status(t, p, 200)
	if m := p.json(); m["resourceId"] != res || m["role"] != nil {
		t.Fatalf("permesso di carol = %s", p.body)
	}

	// L'admin di sistema assegna admin ad alice.
	g := e.do("POST", base+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID("alice"), "role": "admin"}, rootCk)
	status(t, g, 201)
	if m := g.json(); m["role"] != "admin" || m["subjectType"] != "user" || m["resourceId"] != res || m["id"] == nil {
		t.Fatalf("grant = %s", g.body)
	}
	// Secondo grant per lo stesso soggetto: 409.
	errCode(t, e.do("POST", base+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID("alice"), "role": "read"}, rootCk), 409, "already_exists")
	// Soggetto inesistente e ruolo non valido: 422.
	errCode(t, e.do("POST", base+"/grants", map[string]any{"subjectType": "team", "subjectId": uuid.NewString(), "role": "read"}, rootCk), 422, "validation_failed")
	errCode(t, e.do("POST", base+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID("carol"), "role": "owner"}, rootCk), 422, "validation_failed")
	// Corpo con campi sconosciuti: 400.
	errCode(t, e.do("POST", base+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID("carol"), "role": "read", "x": 1}, rootCk), 400, "bad_request")

	// Alice (admin sulla risorsa) gestisce i grant: team web in write.
	acme, web := uuid.New(), uuid.New()
	e.sql(`INSERT INTO identity.organizations (id, name) VALUES ($1, 'acme')`, acme)
	e.sql(`INSERT INTO identity.teams (id, org_id, name) VALUES ($1, $2, 'web')`, web, acme)
	e.sql(`INSERT INTO identity.org_members (org_id, user_id) VALUES ($1, $2)`, acme, e.userID("bob"))
	e.sql(`INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, web, acme, e.userID("bob"))
	tg := e.do("POST", base+"/grants", map[string]any{"subjectType": "team", "subjectId": web.String(), "role": "write"}, aliceCk)
	status(t, tg, 201)
	gid := tg.json()["id"].(string)

	bobCk := e.mustLogin("bob")
	if m := e.do("GET", base+"/permissions", nil, bobCk).json(); m["role"] != "write" {
		t.Errorf("bob via team: %v", m)
	}
	if m := e.do("GET", base+"/permissions", nil, aliceCk).json(); m["role"] != "admin" {
		t.Errorf("alice: %v", m)
	}
	// Bob ha solo write: non gestisce i grant.
	errCode(t, e.do("GET", base+"/grants", nil, bobCk), 403, "forbidden")

	l := e.do("GET", base+"/grants?perPage=1", nil, aliceCk)
	status(t, l, 200)
	if m := l.json(); m["total"] != float64(2) || len(m["items"].([]any)) != 1 || m["perPage"] != float64(1) {
		t.Errorf("lista = %s", l.body)
	}

	// Update e delete del grant del team.
	up := e.do("PATCH", base+"/grants/"+gid, map[string]any{"role": "read"}, aliceCk)
	status(t, up, 200)
	if up.json()["role"] != "read" {
		t.Errorf("update = %s", up.body)
	}
	if m := e.do("GET", base+"/permissions", nil, bobCk).json(); m["role"] != "read" {
		t.Errorf("bob dopo l'abbassamento: %v", m)
	}
	errCode(t, e.do("PATCH", base+"/grants/"+gid, map[string]any{"role": "boss"}, aliceCk), 422, "validation_failed")
	errCode(t, e.do("PATCH", "/resources/"+uuid.NewString()+"/grants/"+gid, map[string]any{"role": "read"}, rootCk), 404, "not_found")
	status(t, e.do("DELETE", base+"/grants/"+gid, nil, aliceCk), 204)
	errCode(t, e.do("DELETE", base+"/grants/"+gid, nil, aliceCk), 404, "not_found")
	if m := e.do("GET", base+"/permissions", nil, bobCk).json(); m["role"] != nil {
		t.Errorf("bob dopo la revoca: %v", m)
	}
}

func TestInternalPermissionsCheck(t *testing.T) {
	e := newPermEnv(t)
	e.mk("alice", false)
	e.mk("root", true)
	res := uuid.New()
	e.sql(`INSERT INTO identity.resource_grants (id, resource_id, user_id, role) VALUES ($1, $2, $3, 'write')`, uuid.New(), res, e.userID("alice"))

	check := func(user, role string, hdr ...string) resp {
		return e.do("POST", "/internal/permissions/check", map[string]any{"userId": user, "resourceId": res.String(), "role": role}, nil, hdr...)
	}
	// Serve il segreto di servizio.
	errCode(t, check(e.userID("alice"), "read"), 401, "unauthenticated")
	errCode(t, check(e.userID("alice"), "read", bearer("sbagliato")...), 401, "unauthenticated")

	cases := []struct {
		user, role string
		allowed    bool
		effective  any
	}{
		{e.userID("alice"), "read", true, "write"},
		{e.userID("alice"), "write", true, "write"},
		{e.userID("alice"), "admin", false, "write"},
		{e.userID("root"), "admin", true, "admin"},
		{uuid.NewString(), "read", false, nil}, // utente sconosciuto: negato, non errore
	}
	for _, c := range cases {
		r := check(c.user, c.role, bearer(secret)...)
		status(t, r, 200)
		if m := r.json(); m["allowed"] != c.allowed || m["effectiveRole"] != c.effective {
			t.Errorf("%s %s: %s", c.user, c.role, r.body)
		}
	}
	errCode(t, check(e.userID("alice"), "owner", bearer(secret)...), 422, "validation_failed")
	errCode(t, e.do("POST", "/internal/permissions/check", map[string]any{"userId": "x"}, nil, bearer(secret)...), 400, "bad_request")
}
