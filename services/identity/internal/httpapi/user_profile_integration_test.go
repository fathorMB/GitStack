//go:build integration

package httpapi_test

import (
	"bytes"
	"log/slog"
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

// POST /internal/users/profile (v3-a, GIT-347): nome, email e team per le intestazioni firmate
// del login davanti alle app. Solo il servizio la chiama.
func TestLookupUserProfileOverHTTP(t *testing.T) {
	var logs bytes.Buffer
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	h := httpapi.New(svc, slog.New(slog.NewTextHandler(&logs, nil)),
		httpapi.WithOrgs(orgs.New(pool, c.now)), httpapi.WithServiceSecret(secret))
	e := &env{t: t, h: h, clock: c, users: us}
	e.mk("alice", false)
	e.mk("bob", false)
	alice := e.mustLogin("alice")
	status(t, e.do("POST", "/orgs", map[string]any{"name": "acme"}, alice), 201)
	status(t, e.do("POST", "/orgs/acme/teams", map[string]string{"name": "web"}, alice), 201)
	status(t, e.do("POST", "/orgs/acme/teams", map[string]string{"name": "api"}, alice), 201)
	status(t, e.do("PUT", "/orgs/acme/teams/web/members/alice", map[string]string{"role": "member"}, alice), 200)
	status(t, e.do("PUT", "/orgs/acme/teams/api/members/alice", map[string]string{"role": "member"}, alice), 200)

	body := map[string]any{"userId": e.userID("alice")}
	errCode(t, e.do("POST", "/internal/users/profile", body, nil), 401, "unauthenticated")

	r := e.do("POST", "/internal/users/profile", body, nil, bearer(secret)...)
	status(t, r, 200)
	j := r.json()
	teams, _ := j["teams"].([]any)
	if j["username"] != "alice" || j["email"] != "alice@example.com" || j["active"] != true || len(teams) != 2 ||
		teams[0] != "acme/api" || teams[1] != "acme/web" {
		t.Fatalf("profilo di alice: %s", r.body)
	}

	// Senza team l'elenco è vuoto, non assente.
	r = e.do("POST", "/internal/users/profile", map[string]any{"userId": e.userID("bob")}, nil, bearer(secret)...)
	status(t, r, 200)
	if got, ok := r.json()["teams"].([]any); !ok || len(got) != 0 {
		t.Fatalf("bob senza team: %s", r.body)
	}

	errCode(t, e.do("POST", "/internal/users/profile", map[string]any{"userId": "11111111-1111-1111-1111-111111111111"}, nil, bearer(secret)...), 404, "not_found")
	errCode(t, e.do("POST", "/internal/users/profile", map[string]any{}, nil, bearer(secret)...), 400, "bad_request")
}
