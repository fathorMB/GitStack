//go:build integration

package httpapi_test

import (
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
