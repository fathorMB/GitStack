//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

const pw2 = "una password lunga e buona"

type orgEnv struct {
	t     *testing.T
	h     http.Handler
	clock *clock
	users *users.Service
	orgs  *orgs.Service
}

func newOrgEnv(t *testing.T, cfg loginlimit.Config) *orgEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	os := orgs.New(pool, c.now)
	e := &orgEnv{t: t, h: httpapi.New(svc, nil, httpapi.WithOrgs(os)), clock: c, users: us, orgs: os}
	return e
}

func (e *orgEnv) mk(name string, admin bool) {
	e.t.Helper()
	_, err := e.users.Create(context.Background(), users.CreateInput{
		Username: name, Email: name + "@example.com", DisplayName: name, Password: pw2, IsAdmin: admin,
	})
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
}

func (e *orgEnv) do(method, path string, body any, ck *http.Cookie, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.10:5555"
	if ck != nil {
		req.AddCookie(ck)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return resp{res, b}
}

func (e *orgEnv) mustLogin(name string) *http.Cookie {
	e.t.Helper()
	c, _ := e.login(name, pw2)
	return c
}

func status(t *testing.T, r resp, want int) {
	t.Helper()
	if r.StatusCode != want {
		t.Fatalf("status %d atteso %d: %s", r.StatusCode, want, r.body)
	}
}

func errCode(t *testing.T, r resp, wantStatus int, wantCode string) {
	t.Helper()
	if r.StatusCode != wantStatus {
		t.Fatalf("status %d atteso %d: %s", r.StatusCode, wantStatus, r.body)
	}
	m := r.json()
	ie, ok := m["error"]
	if !ok {
		t.Fatalf("nessun campo error: %s", r.body)
	}
	e := ie.(map[string]any)
	if e["code"] != wantCode {
		t.Fatalf("codice %q atteso %q: %s", e["code"], wantCode, r.body)
	}
}

// ---- Rule 1: unique name, validation ----

func TestOrgCreateUniqueAndValidation(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	cookie := e.mustLogin("root")

	// Crea prima org.
	r := e.do("POST", "/orgs", map[string]string{"name": "myorg"}, cookie)
	status(t, r, 201)

	// Seconda con lo stesso nome → 409.
	r = e.do("POST", "/orgs", map[string]string{"name": "myorg"}, cookie)
	errCode(t, r, 409, "already_exists")

	// Nome con caratteri non validi → 422.
	r = e.do("POST", "/orgs", map[string]string{"name": "Bad_Name"}, cookie)
	errCode(t, r, 422, "validation_failed")
	r = e.do("POST", "/orgs", map[string]string{"name": "ab--"}, cookie)
	errCode(t, r, 422, "validation_failed")
	r = e.do("POST", "/orgs", map[string]string{"name": "-start"}, cookie)
	errCode(t, r, 422, "validation_failed")
}

// ---- Rule 2: creator becomes owner ----

func TestOrgCreatorBecomesOwner(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	cookie := e.mustLogin("root")

	e.do("POST", "/orgs", map[string]string{"name": "acme"}, cookie)
	status(t, e.do("GET", "/orgs/acme", cookie), 200)
	members := e.do("GET", "/orgs/acme/members", cookie)
	status(t, members, 200)
	body := members.json()
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("atteso 1 membro, trovato %d: %s", len(items), members.body)
	}
	u := items[0].(map[string]any)
	if u["role"] != "owner" || u["username"] != "root" {
		t.Fatalf("ruolo inatteso: %v", u)
	}
}

// ---- Rule 3: last owner protection (with concurrent test) ----

func TestLastOwnerProtection(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	rootCookie := e.mustLogin("root")
	aliceCookie := e.mustLogin("alice")
	bobCookie := e.mustLogin("bob")

	// root crea org e diventa owner.
	e.do("POST", "/orgs", map[string]string{"name": "proj"}, rootCookie)

	// root aggiunge alice e bob come owner.
	e.do("PUT", "/orgs/proj/members/alice", map[string]string{"role": "owner"}, rootCookie)
	e.do("PUT", "/orgs/proj/members/bob", map[string]string{"role": "owner"}, rootCookie)

	// root declassa alice → ok.
	e.do("PUT", "/orgs/proj/members/alice", map[string]string{"role": "member"}, rootCookie)
	status(t, e.do("GET", "/orgs/proj", rootCookie), 200)

	// ora solo root è owner: declassare root → 409.
	r := e.do("PUT", "/orgs/proj/members/root", map[string]string{"role": "member"}, rootCookie)
	errCode(t, r, 409, "last_owner")

	// root ripristina.
	e.do("PUT", "/orgs/proj/members/root", map[string]string{"role": "owner"}, rootCookie)
}

func TestLastOwnerConcurrent(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	rootCookie := e.mustLogin("root")

	// root crea org.
	e.do("POST", "/orgs", map[string]string{"name": "proj2"}, rootCookie)

	// root promuove alice e bob a owner.
	e.do("PUT", "/orgs/proj2/members/alice", map[string]string{"role": "owner"}, rootCookie)
	e.do("PUT", "/orgs/proj2/members/bob", map[string]string{"role": "owner"}, rootCookie)

	// root si declassa a member: deve funzionare (alice e bob sono ancora owner).
	// Poi alice e bob tentano di declassarsi concorrentemente → solo uno ha successo.
	var wg sync.WaitGroup
	var successBob, successAlice int
	var mu sync.Mutex

	wg.Add(2)

	go func() {
		defer wg.Done()
		// alice tenta di declassarsi.
		r := e.do("PUT", "/orgs/proj2/members/alice", map[string]string{"role": "member"}, e.mustLogin("alice"))
		if r.StatusCode == 200 {
			mu.Lock()
			successAlice++
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		// bob tenta di declassarsi.
		r := e.do("PUT", "/orgs/proj2/members/bob", map[string]string{"role": "member"}, e.mustLogin("bob"))
		if r.StatusCode == 200 {
			mu.Lock()
			successBob++
			mu.Unlock()
		}
	}()

	wg.Wait()

	if successAlice+successBob != 1 {
		t.Fatalf("atteso 1 successo concorrente, ottenuti %d+%d", successAlice, successBob)
	}
}

// ---- Rule 4: member/fuori organizza → 403 ----

func TestOrgMemberPermissions(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	rootCookie := e.mustLogin("root")
	aliceCookie := e.mustLogin("alice")
	bobCookie := e.mustLogin("bob")

	// root crea org.
	e.do("POST", "/orgs", map[string]string{"name": "proj3"}, rootCookie)

	// bob (fuori org) non può accedere.
	errCode(t, e.do("GET", "/orgs/proj3", bobCookie), 403, "forbidden")
	errCode(t, e.do("GET", "/orgs/proj3/teams", bobCookie), 403, "forbidden")

	// alice (fuori org) non può modificare membri.
	errCode(t, e.do("PUT", "/orgs/proj3/members/bob", map[string]string{"role": "member"}, aliceCookie), 403, "forbidden")
	errCode(t, e.do("POST", "/orgs/proj3/teams", map[string]string{"name": "dev"}, aliceCookie), 403, "forbidden")

	// root (admin) può tutto.
	r := e.do("POST", "/orgs/proj3/teams", map[string]string{"name": "dev"}, rootCookie)
	status(t, r, 201)

	// root (owner di org) può anche gestire membri.
	r = e.do("PUT", "/orgs/proj3/members/bob", map[string]string{"role": "member"}, rootCookie)
	status(t, r, 200)
}

// ---- Rule 5: team membership validation ----

func TestTeamMemberOrgValidation(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	rootCookie := e.mustLogin("root")

	// root crea org e team.
	e.do("POST", "/orgs", map[string]string{"name": "proj4"}, rootCookie)
	e.do("POST", "/orgs/proj4/teams", map[string]string{"name": "dev"}, rootCookie)

	// Solo root è membro. bob non è membro dell'org.
	r := e.do("PUT", "/orgs/proj4/teams/dev/members/bob", map[string]string{"role": "member"}, rootCookie)
	errCode(t, r, 422, "not_org_member")

	// root diventa anche membro di bob.
	e.do("PUT", "/orgs/proj4/members/bob", map[string]string{"role": "member"}, rootCookie)
	// Ora bob è membro dell'org: può essere aggiunto al team.
	r = e.do("PUT", "/orgs/proj4/teams/dev/members/bob", map[string]string{"role": "member"}, rootCookie)
	status(t, r, 200)
}

// ---- Rule 6: error codes conform to schema ----

func TestErrorCodes(t *testing.T) {
	e := newOrgEnv(t, cfg)
	e.mk("root", true)
	cookie := e.mustLogin("root")

	// 401 per non autenticato.
	r := e.do("GET", "/orgs", nil, nil)
	errCode(t, r, 401, "unauthenticated")

	// 404 per org inesistente.
	r = e.do("GET", "/orgs/nessuna", cookie)
	errCode(t, r, 404, "not_found")

	// 422 per nome non valido.
	r = e.do("POST", "/orgs", map[string]string{"name": "X"}, cookie)
	errCode(t, r, 422, "validation_failed")

	// 409 per duplicato.
	e.do("POST", "/orgs", map[string]string{"name": "dup"}, cookie)
	r = e.do("POST", "/orgs", map[string]string{"name": "dup"}, cookie)
	errCode(t, r, 409, "already_exists")

	// 400 per pagina invalida.
	r = e.do("GET", "/orgs?perPage=0", cookie)
	errCode(t, r, 400, "bad_request")
}
