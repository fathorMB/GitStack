//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

const pw = "una password lunga e buona"

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	t     *testing.T
	h     http.Handler
	clock *clock
	users *users.Service
}

func newEnv(t *testing.T, cfg loginlimit.Config) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	return &env{t: t, h: httpapi.New(svc, nil), clock: c, users: us}
}

var cfg = loginlimit.Config{MaxPerUser: 3, MaxPerIP: 100, Window: 10 * time.Minute}

func (e *env) mk(name string, admin bool) {
	e.t.Helper()
	_, err := e.users.Create(context.Background(), users.CreateInput{Username: name, Email: name + "@example.com", DisplayName: name, Password: pw, IsAdmin: admin})
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
}

type resp struct {
	*http.Response
	body []byte
}

func (r resp) json() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		panic("corpo non JSON: " + string(r.body))
	}
	return m
}

func (e *env) do(method, path string, body any, ck *http.Cookie, hdr ...string) resp {
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

func (e *env) login(name, pass string) (*http.Cookie, resp) {
	e.t.Helper()
	r := e.do("POST", "/auth/login", map[string]string{"username": name, "password": pass}, nil)
	for _, c := range r.Cookies() {
		if c.Name == "gst_session" {
			return c, r
		}
	}
	return nil, r
}

func (e *env) mustLogin(name string) *http.Cookie {
	e.t.Helper()
	ck, r := e.login(name, pw)
	if ck == nil {
		e.t.Fatalf("login %s: %d %s", name, r.StatusCode, r.body)
	}
	return ck
}

func status(t *testing.T, r resp, want int) {
	t.Helper()
	if r.StatusCode != want {
		t.Fatalf("status %d, atteso %d: %s", r.StatusCode, want, r.body)
	}
}

func errCode(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.StatusCode != status {
		t.Fatalf("status %d, atteso %d: %s", r.StatusCode, status, r.body)
	}
	e, _ := r.json()["error"].(map[string]any)
	if e == nil || e["code"] != code || e["message"] == "" {
		t.Fatalf("corpo d'errore inatteso (atteso codice %q): %s", code, r.body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type %q", ct)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("alice", false)
	ck, r := e.login("alice", pw)
	status(t, r, 200)
	if ck == nil {
		t.Fatal("nessun Set-Cookie gst_session")
	}
	// Attributi del cookie letti dalla risposta.
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Value == "" || ck.Expires.IsZero() {
		t.Fatalf("attributi del cookie errati: %+v", ck)
	}
	raw := r.Header.Get("Set-Cookie")
	for _, a := range []string{"HttpOnly", "Secure", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(raw, a) {
			t.Fatalf("Set-Cookie senza %q: %s", a, raw)
		}
	}
	// Corpo: CurrentSession, senza il valore del cookie né la password.
	m := r.json()
	u, _ := m["user"].(map[string]any)
	if m["authMethod"] != "password" || m["expiresAt"] == nil || u["username"] != "alice" || u["kind"] != "human" ||
		u["email"] != "alice@example.com" || u["isActive"] != true || u["isAdmin"] != false || u["id"] == nil || u["createdAt"] == nil {
		t.Fatalf("CurrentSession inattesa: %s", r.body)
	}
	if strings.Contains(string(r.body), ck.Value) || strings.Contains(string(r.body), pw) || strings.Contains(string(r.body), "argon2") {
		t.Fatal("il corpo contiene segreti")
	}
	// Login anche con l'email.
	if _, r := e.login("Alice@Example.com", pw); r.StatusCode != 200 {
		t.Fatalf("login via email: %d", r.StatusCode)
	}
	// Validazione e formato.
	r = e.do("POST", "/auth/login", map[string]string{"username": "alice"}, nil)
	errCode(t, r, 422, "validation_failed")
	if f := r.json()["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any); f["password"] == nil {
		t.Fatalf("details.fields senza password: %s", r.body)
	}
	errCode(t, e.do("POST", "/auth/login", "{non json", nil), 400, "bad_request")
	errCode(t, e.do("POST", "/auth/login", `{"username":"a","password":"b","extra":1}`, nil), 400, "bad_request")
}

func TestLoginSameResponseWrongPasswordUnknownUser(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 100, MaxPerIP: 100, Window: time.Hour})
	e.mk("alice", false)
	e.mk("bob", true)
	f := false
	if _, err := e.users.Update(context.Background(), "alice", users.UpdateInput{IsActive: &f}); err != nil {
		t.Fatal(err)
	}
	_, wrong := e.login("bob", "password sbagliata!!")
	_, unknown := e.login("nessuno", "password sbagliata!!")
	_, inactive := e.login("alice", pw)
	errCode(t, wrong, 401, "invalid_credentials")
	for name, r := range map[string]resp{"inesistente": unknown, "disattivato": inactive} {
		if r.StatusCode != wrong.StatusCode || !bytes.Equal(r.body, wrong.body) {
			t.Fatalf("%s: risposta diversa da password errata:\n%d %s\n%d %s", name, r.StatusCode, r.body, wrong.StatusCode, wrong.body)
		}
		if len(r.Cookies()) != 0 {
			t.Fatalf("%s: cookie impostato", name)
		}
	}
}

func TestLoginRateLimit429AndUnblock(t *testing.T) {
	e := newEnv(t, cfg) // 3 tentativi per utente, 10 minuti
	e.mk("alice", false)
	for i := 0; i < 3; i++ {
		_, r := e.login("alice", "sbagliata sbagliata")
		errCode(t, r, 401, "invalid_credentials")
	}
	_, r := e.login("alice", pw)
	errCode(t, r, 429, "too_many_attempts")
	secs, err := strconv.Atoi(r.Header.Get("Retry-After"))
	if err != nil || secs < 1 || secs > 600 {
		t.Fatalf("Retry-After = %q", r.Header.Get("Retry-After"))
	}
	e.clock.advance(time.Duration(secs) * time.Second)
	if ck, r := e.login("alice", pw); ck == nil {
		t.Fatalf("dopo Retry-After il login deve riuscire: %d %s", r.StatusCode, r.body)
	}
}

func TestRateLimitPerIPIgnoresXForwardedFor(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 100, MaxPerIP: 3, Window: time.Hour})
	e.mk("alice", false)
	for i := 0; i < 3; i++ {
		r := e.do("POST", "/auth/login", map[string]string{"username": "u" + strconv.Itoa(i), "password": "sbagliata sbagliata"}, nil,
			"X-Forwarded-For", "203.0.113."+strconv.Itoa(i))
		errCode(t, r, 401, "invalid_credentials")
	}
	r := e.do("POST", "/auth/login", map[string]string{"username": "alice", "password": pw}, nil, "X-Forwarded-For", "198.51.100.77")
	errCode(t, r, 429, "too_many_attempts")
}

func TestSessionAndLogout(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("alice", false)
	errCode(t, e.do("GET", "/auth/session", nil, nil), 401, "unauthenticated")
	errCode(t, e.do("POST", "/auth/logout", nil, nil), 401, "unauthenticated")
	ck := e.mustLogin("alice")
	other := e.mustLogin("alice")

	r := e.do("GET", "/auth/session", nil, ck)
	status(t, r, 200)
	if m := r.json(); m["authMethod"] != "password" || m["user"].(map[string]any)["username"] != "alice" {
		t.Fatalf("sessione: %s", r.body)
	}

	r = e.do("POST", "/auth/logout", nil, ck)
	status(t, r, 204)
	if len(r.body) != 0 {
		t.Fatal("204 con corpo")
	}
	var cleared *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == "gst_session" {
			cleared = c
		}
	}
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.HttpOnly || !cleared.Secure || cleared.SameSite != http.SameSiteLaxMode || cleared.Path != "/" {
		t.Fatalf("cookie di logout errato: %+v", cleared)
	}
	errCode(t, e.do("GET", "/auth/session", nil, ck), 401, "unauthenticated")
	errCode(t, e.do("POST", "/auth/logout", nil, ck), 401, "unauthenticated")
	status(t, e.do("GET", "/auth/session", nil, other), 200) // solo la corrente è revocata

	// Scadenza.
	e.clock.advance(2 * time.Hour)
	errCode(t, e.do("GET", "/auth/session", nil, other), 401, "unauthenticated")
}

// Senza WithOIDC (nessun file di configurazione) il login OIDC è spento.
func TestOIDCOffWithoutConfig(t *testing.T) {
	e := newEnv(t, cfg)
	if r := e.do("GET", "/auth/oidc/providers", nil, nil); r.StatusCode != 200 {
		t.Errorf("providers: status %d", r.StatusCode)
	}
	for _, p := range []string{"/auth/oidc/google/start", "/auth/oidc/google/callback?code=x&state=y"} {
		errCode(t, e.do("GET", p, nil, nil), 404, "not_found")
	}
}

func TestCreateUser(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	body := map[string]any{"username": "carol", "email": "carol@example.com", "displayName": "Carol", "password": pw}

	errCode(t, e.do("POST", "/users", body, nil), 401, "unauthenticated")
	errCode(t, e.do("POST", "/users", body, e.mustLogin("alice")), 403, "forbidden")

	root := e.mustLogin("root")
	r := e.do("POST", "/users", body, root)
	status(t, r, 201)
	if r.Header.Get("Location") != "/v1/users/carol" {
		t.Fatalf("Location = %q", r.Header.Get("Location"))
	}
	m := r.json()
	if m["username"] != "carol" || m["kind"] != "human" || m["isActive"] != true || m["email"] != "carol@example.com" || m["id"] == nil {
		t.Fatalf("User inatteso: %s", r.body)
	}
	if strings.Contains(string(r.body), "password") || strings.Contains(string(r.body), pw) {
		t.Fatal("password nel corpo")
	}
	if _, lr := e.login("carol", pw); lr.StatusCode != 200 {
		t.Fatal("il nuovo utente non riesce ad accedere")
	}
	// Agente senza password.
	status(t, e.do("POST", "/users", map[string]any{"username": "bot", "kind": "agent"}, root), 201)

	errCode(t, e.do("POST", "/users", body, root), 409, "already_exists")
	errCode(t, e.do("POST", "/users", map[string]any{"username": "dave", "password": "corta"}, root), 422, "validation_failed")
	errCode(t, e.do("POST", "/users", map[string]any{"username": "Bad_Name"}, root), 422, "validation_failed")
	errCode(t, e.do("POST", "/users", `{"username":`, root), 400, "bad_request")
}

func TestListAndGetUsers(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	errCode(t, e.do("GET", "/users", nil, nil), 401, "unauthenticated")
	errCode(t, e.do("GET", "/users/alice", nil, nil), 401, "unauthenticated")

	alice, root := e.mustLogin("alice"), e.mustLogin("root")
	r := e.do("GET", "/users?perPage=2&page=1", nil, alice)
	status(t, r, 200)
	m := r.json()
	items := m["items"].([]any)
	if m["total"].(float64) != 3 || m["page"].(float64) != 1 || m["perPage"].(float64) != 2 || len(items) != 2 {
		t.Fatalf("UserList inattesa: %s", r.body)
	}
	for _, it := range items { // alice vede l'email solo di sé
		u := it.(map[string]any)
		if _, has := u["email"]; has != (u["username"] == "alice") {
			t.Fatalf("visibilità email errata per %v: %s", u["username"], r.body)
		}
	}
	r = e.do("GET", "/users?q=bo", nil, root)
	if it := r.json()["items"].([]any); len(it) != 1 || it[0].(map[string]any)["email"] != "bob@example.com" {
		t.Fatalf("l'admin deve vedere l'email: %s", r.body)
	}
	errCode(t, e.do("GET", "/users?perPage=1000", nil, alice), 400, "bad_request")

	// getUser: profilo pubblico per altri, completo per sé e per gli admin.
	if u := e.do("GET", "/users/bob", nil, alice).json(); u["username"] != "bob" || u["email"] != nil || u["isAdmin"] != nil {
		t.Fatalf("profilo pubblico con campi riservati: %v", u)
	}
	if u := e.do("GET", "/users/alice", nil, alice).json(); u["email"] != "alice@example.com" || u["isAdmin"] != false {
		t.Fatalf("profilo proprio incompleto: %v", u)
	}
	if u := e.do("GET", "/users/bob", nil, root).json(); u["email"] != "bob@example.com" || u["isActive"] != true {
		t.Fatalf("l'admin deve vedere tutto: %v", u)
	}
	errCode(t, e.do("GET", "/users/nessuno", nil, alice), 404, "not_found")
}

func TestUpdateUser(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	alice, root := e.mustLogin("alice"), e.mustLogin("root")
	patch := map[string]any{"displayName": "Alice A.", "bio": "ciao", "avatarUrl": "https://example.com/a.png"}

	errCode(t, e.do("PATCH", "/users/alice", patch, nil), 401, "unauthenticated")
	errCode(t, e.do("PATCH", "/users/bob", patch, alice), 403, "forbidden")
	errCode(t, e.do("PATCH", "/users/nessuno", patch, alice), 403, "forbidden") // non rivela l'esistenza
	errCode(t, e.do("PATCH", "/users/alice", map[string]any{"isAdmin": true}, alice), 403, "forbidden")
	errCode(t, e.do("PATCH", "/users/alice", map[string]any{}, alice), 400, "bad_request")
	errCode(t, e.do("PATCH", "/users/alice", map[string]any{"avatarUrl": "ftp://x"}, alice), 422, "validation_failed")

	r := e.do("PATCH", "/users/alice", patch, alice)
	status(t, r, 200)
	if u := r.json(); u["displayName"] != "Alice A." || u["bio"] != "ciao" || u["avatarUrl"] != "https://example.com/a.png" {
		t.Fatalf("User aggiornato: %s", r.body)
	}
	if u := e.do("PATCH", "/users/alice", map[string]any{"avatarUrl": nil}, alice).json(); u["avatarUrl"] != nil {
		t.Fatalf("avatarUrl null non cancella: %v", u)
	}
	errCode(t, e.do("PATCH", "/users/nessuno", patch, root), 404, "not_found")

	// L'admin promuove bob; ultimo admin protetto.
	if u := e.do("PATCH", "/users/bob", map[string]any{"isAdmin": true}, root).json(); u["isAdmin"] != true {
		t.Fatalf("promozione: %v", u)
	}
	e.do("PATCH", "/users/bob", map[string]any{"isAdmin": false}, root)
	errCode(t, e.do("PATCH", "/users/root", map[string]any{"isAdmin": false}, root), 409, "last_admin")
	errCode(t, e.do("PATCH", "/users/root", map[string]any{"isActive": false}, root), 409, "last_admin")
}

func TestDeactivateInvalidatesSession(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	alice, root := e.mustLogin("alice"), e.mustLogin("root")
	status(t, e.do("GET", "/auth/session", nil, alice), 200)
	if u := e.do("PATCH", "/users/alice", map[string]any{"isActive": false}, root).json(); u["isActive"] != false {
		t.Fatalf("disattivazione: %v", u)
	}
	errCode(t, e.do("GET", "/auth/session", nil, alice), 401, "unauthenticated")
	if _, r := e.login("alice", pw); r.StatusCode != 401 {
		t.Fatalf("un utente disattivato non deve accedere: %d", r.StatusCode)
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	alice, root := e.mustLogin("alice"), e.mustLogin("root")
	errCode(t, e.do("DELETE", "/users/alice", nil, nil), 401, "unauthenticated")
	errCode(t, e.do("DELETE", "/users/root", nil, alice), 403, "forbidden")
	errCode(t, e.do("DELETE", "/users/root", nil, root), 409, "last_admin")
	r := e.do("DELETE", "/users/alice", nil, root)
	status(t, r, 204)
	if len(r.body) != 0 {
		t.Fatal("204 con corpo")
	}
	errCode(t, e.do("DELETE", "/users/alice", nil, root), 404, "not_found")
	errCode(t, e.do("GET", "/auth/session", nil, alice), 401, "unauthenticated")
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("root", true)
	e.mk("alice", false)
	e.mk("bob", false)
	alice, aliceOther, root := e.mustLogin("alice"), e.mustLogin("alice"), e.mustLogin("root")
	const newPw = "nuova password lunga"

	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"newPassword": newPw}, nil), 401, "unauthenticated")
	errCode(t, e.do("PUT", "/users/bob/password", map[string]any{"currentPassword": pw, "newPassword": newPw}, alice), 403, "forbidden")
	// Password attuale errata: 403 forbidden (non 401), nulla cambia.
	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": "sbagliata sbagliata", "newPassword": newPw}, alice), 403, "forbidden")
	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": pw, "newPassword": "corta"}, alice), 422, "validation_failed")
	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": pw}, alice), 422, "validation_failed")
	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"newPassword": newPw}, alice), 422, "validation_failed") // manca currentPassword
	status(t, e.do("GET", "/auth/session", nil, aliceOther), 200)

	r := e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": pw, "newPassword": newPw}, alice)
	status(t, r, 204)
	status(t, e.do("GET", "/auth/session", nil, alice), 200) // la sessione del chiamante resta
	errCode(t, e.do("GET", "/auth/session", nil, aliceOther), 401, "unauthenticated")
	if _, r := e.login("alice", pw); r.StatusCode != 401 {
		t.Fatal("la vecchia password funziona ancora")
	}
	if ck, _ := e.login("alice", newPw); ck == nil {
		t.Fatal("la nuova password non funziona")
	}

	// L'admin cambia la password di bob senza quella attuale: revoca tutte le sue sessioni.
	bob := e.mustLogin("bob")
	status(t, e.do("PUT", "/users/bob/password", map[string]any{"newPassword": newPw}, root), 204)
	errCode(t, e.do("GET", "/auth/session", nil, bob), 401, "unauthenticated")
	errCode(t, e.do("PUT", "/users/nessuno/password", map[string]any{"newPassword": newPw}, root), 404, "not_found")
}

// changePassword con password attuale errata ripetuta: dopo MaxPerUser
// tentativi 429 con Retry-After, anche con la password giusta.
func TestChangePasswordRateLimited(t *testing.T) {
	e := newEnv(t, cfg) // MaxPerUser = 3
	e.mk("alice", false)
	alice := e.mustLogin("alice")
	body := map[string]any{"currentPassword": "sbagliata sbagliata", "newPassword": "nuova password lunga"}
	for i := 0; i < cfg.MaxPerUser; i++ {
		errCode(t, e.do("PUT", "/users/alice/password", body, alice), 403, "forbidden")
	}
	r := e.do("PUT", "/users/alice/password", body, alice)
	errCode(t, r, 429, "too_many_attempts")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("manca Retry-After")
	}
	// Anche la password giusta è respinta finché il limite è attivo.
	errCode(t, e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": pw, "newPassword": "nuova password lunga"}, alice), 429, "too_many_attempts")
	// Scaduta la finestra si può di nuovo.
	e.clock.advance(cfg.Window + time.Second)
	status(t, e.do("PUT", "/users/alice/password", map[string]any{"currentPassword": pw, "newPassword": "nuova password lunga"}, alice), 204)
}

// Il limite per IP usa l'header del gateway solo da proxy fidati.
func TestLoginIPFidato(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour),
		Limiter: loginlimit.New(loginlimit.Config{MaxPerUser: 100, MaxPerIP: 2, Window: time.Minute}, c.now)}
	_, gw, _ := net.ParseCIDR("192.0.2.0/24")
	h := httpapi.New(svc, nil, httpapi.WithTrustedProxies([]*net.IPNet{gw}))
	try := func(ip string) int {
		req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"username":"x","password":"y"}`))
		req.RemoteAddr = "192.0.2.10:1"
		req.Header.Set(httpapi.ClientIPHeader, ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	try("203.0.113.1")
	try("203.0.113.1")
	if got := try("203.0.113.1"); got != 429 {
		t.Fatalf("stesso client: %d, atteso 429", got)
	}
	if got := try("203.0.113.2"); got != 401 {
		t.Fatalf("altro client: %d, atteso 401 (non bloccato)", got)
	}
}
