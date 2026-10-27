//go:build integration

package httpapi_test

import (
	"context"
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
)

func TestListOrgOwnersOverHTTP(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	h := httpapi.New(svc, nil, httpapi.WithOrgs(orgs.New(pool, c.now)), httpapi.WithServiceSecret(secret))
	e := &env{t: t, h: h, clock: c, users: us}
	e.mk("alice", false)
	e.mk("bob", false)
	alice := e.mustLogin("alice")
	r := e.do("POST", "/orgs", map[string]any{"name": "acme"}, alice)
	status(t, r, 201)
	orgID, _ := r.json()["id"].(string)
	status(t, e.do("PUT", "/orgs/acme/members/bob", map[string]string{"role": "member"}, alice), 200)

	path := "/internal/orgs/" + orgID + "/owners"
	errCode(t, e.do("GET", path, nil, nil), 401, "unauthenticated")
	r = e.do("GET", path, nil, nil, bearer(secret)...)
	status(t, r, 200)
	owners, _ := r.json()["owners"].([]any)
	if len(owners) != 1 || owners[0] != e.userID("alice") {
		t.Fatalf("attesto solo alice come owner: %s", r.body)
	}
	errCode(t, e.do("GET", "/internal/orgs/11111111-1111-1111-1111-111111111111/owners", nil, nil, bearer(secret)...), 404, "not_found")
}

// GIT-404: GET /internal/resources/{id}/members, solo con il segreto di servizio.
func TestListResourceMembersOverHTTP(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	h := httpapi.New(svc, nil, httpapi.WithPermissions(permissions.New(pool, c.now)), httpapi.WithServiceSecret(secret))
	e := &env{t: t, h: h, clock: c, users: us}
	e.mk("alice", true)
	e.mk("bob", false)
	res := "22222222-2222-2222-2222-222222222222"
	if _, err := pool.Exec(context.Background(), `INSERT INTO identity.resource_grants (resource_id, user_id, role) VALUES ($1, $2, 'write')`, res, e.userID("bob")); err != nil {
		t.Fatal(err)
	}

	path := "/internal/resources/" + res + "/members?role=admin"
	errCode(t, e.do("GET", path, nil, nil), 401, "unauthenticated")
	alice := e.mustLogin("alice")
	errCode(t, e.do("GET", path, nil, alice), 401, "unauthenticated") // una sessione utente non basta
	r := e.do("GET", path, nil, nil, bearer(secret)...)
	status(t, r, 200)
	got, _ := r.json()["userIds"].([]any)
	if len(got) != 1 || got[0] != e.userID("alice") {
		t.Fatalf("admin = solo alice (amministratore di sistema): %s", r.body)
	}
	r = e.do("GET", "/internal/resources/"+res+"/members?role=write", nil, nil, bearer(secret)...)
	status(t, r, 200)
	if got, _ = r.json()["userIds"].([]any); len(got) != 2 {
		t.Fatalf("write = alice e bob: %s", r.body)
	}
	status(t, e.do("GET", "/internal/resources/"+res+"/members?role=boh", nil, nil, bearer(secret)...), 400)
}
