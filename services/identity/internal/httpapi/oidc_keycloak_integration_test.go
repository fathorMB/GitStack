//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/oidc"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// Questo test gira contro un Keycloak vero (realm in testdata/keycloak-realm.json):
// si salta senza GITSTACK_TEST_KEYCLOAK_URL (es. http://localhost:8080). Serve
// anche Postgres (GITSTACK_TEST_DATABASE_URL). Il login sull'IdP si fa senza
// browser: form POST di un http.Client con cookie jar. La redirect_uri
// registrata nel realm è https://git.test/..., che non esiste: il client si
// ferma sul redirect verso di essa e ne legge code e state.

const (
	kcPublicURL = "https://git.test"
	kcSlug      = "keycloak"
)

var kcFormAction = regexp.MustCompile(`<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"`)

func kcSetup(t *testing.T, link, autoCreate bool) (http.Handler, *users.Service, func(string, ...any) int) {
	t.Helper()
	base := os.Getenv("GITSTACK_TEST_KEYCLOAK_URL")
	if base == "" {
		t.Skip("GITSTACK_TEST_KEYCLOAK_URL non impostata: test Keycloak saltato")
	}
	base = strings.TrimRight(base, "/")
	pool, _ := dbtest.NewPool(t)
	us := users.New(pool, nil)
	ss := sessions.New(pool, nil, time.Hour)

	cfgJSON := fmt.Sprintf(`{"providers":[{
		"slug":%q,"displayName":"Keycloak","issuer":%q,"clientId":"gitstack","clientSecret":"keycloak-test-secret",
		"linkByVerifiedEmail":%v,"autoCreateUsers":%v}]}`, kcSlug, base+"/realms/gitstack", link, autoCreate)
	providers, err := oidc.Parse([]byte(cfgJSON), oidc.Options{AllowInsecureIssuer: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := oidc.New(oidc.NewPGStore(pool, us, ss, nil), oidc.Config{
		Providers: providers, Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "test", PublicURL: kcPublicURL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := &auth.Service{Users: us, Sessions: ss, Limiter: loginlimit.New(cfg, time.Now)}
	h := httpapi.New(a, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.WithOIDC(svc))
	count := func(q string, args ...any) int {
		var n int
		if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		return n
	}
	return h, us, count
}

func kcDo(h http.Handler, method, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "198.51.100.7:4711"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// kcStart chiama start sull'handler e ritorna la Location del provider e il
// cookie di stato.
func kcStart(t *testing.T, h http.Handler, redirectTo string) (string, *http.Cookie) {
	t.Helper()
	target := "/auth/oidc/" + kcSlug + "/start"
	if redirectTo != "" {
		target += "?redirectTo=" + url.QueryEscape(redirectTo)
	}
	rec := kcDo(h, http.MethodGet, target)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	c := cookieNamed(rec, oidc.StateCookieName)
	if c == nil || !c.HttpOnly || c.Secure || c.Path != "/api/v1/auth/oidc/"+kcSlug+"/callback" {
		t.Fatalf("cookie di stato inatteso: %+v", c)
	}
	return rec.Header().Get("Location"), c
}

// kcLogin fa login su Keycloak con username e password e ritorna code e state
// del redirect verso la callback.
func kcLogin(t *testing.T, authURL, username, password string) (code, state string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.HasPrefix(req.URL.String(), kcPublicURL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := kcFormAction.FindSubmatch(page)
	if m == nil {
		t.Fatalf("form di login non trovato (HTTP %d): %.300s", resp.StatusCode, page)
	}
	action := html.UnescapeString(string(m[1]))
	resp, err = client.PostForm(action, url.Values{"username": {username}, "password": {password}, "credentialId": {""}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, kcPublicURL+"/api/v1/auth/oidc/"+kcSlug+"/callback?") {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("redirect di ritorno inatteso (HTTP %d, Location %q): %.300s", resp.StatusCode, loc, body)
	}
	u, _ := url.Parse(loc)
	return u.Query().Get("code"), u.Query().Get("state")
}

func kcCallback(h http.Handler, code, state string, c *http.Cookie) *httptest.ResponseRecorder {
	q := url.Values{"code": {code}, "state": {state}}
	return kcDo(h, http.MethodGet, "/auth/oidc/"+kcSlug+"/callback?"+q.Encode(), c)
}

func kcErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct{ Error struct{ Code string } }
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error.Code
}

func TestKeycloak_LoginFlow(t *testing.T) {
	h, us, count := kcSetup(t, true, true)
	ctx := context.Background()

	// I provider configurati sono pubblici e senza segreti.
	rec := kcDo(h, http.MethodGet, "/auth/oidc/providers")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"slug":"keycloak"`) || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("elenco provider: %d %s", rec.Code, rec.Body.String())
	}

	// alice: utente locale con la stessa email, verificata da Keycloak → collegata.
	alice, err := us.Create(ctx, users.CreateInput{Username: "alice-locale", Email: "alice@example.com", DisplayName: "Alice", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	// bob: utente locale con l'email che Keycloak NON ha verificato → mai collegato.
	if _, err := us.Create(ctx, users.CreateInput{Username: "bob-locale", Email: "bob@example.com", Password: pw}); err != nil {
		t.Fatal(err)
	}

	t.Run("collegamento per email verificata e sessione", func(t *testing.T) {
		authURL, sc := kcStart(t, h, "/repos")
		code, state := kcLogin(t, authURL, "alice", "alice-password")
		rec := kcCallback(h, code, state, sc)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/repos" {
			t.Fatalf("callback: %d Location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		sess := cookieNamed(rec, auth.CookieName)
		if sess == nil || !sess.HttpOnly || sess.Secure {
			t.Fatalf("cookie di sessione assente o non sicuro: %+v", sess)
		}
		if c := cookieNamed(rec, oidc.StateCookieName); c == nil || c.MaxAge >= 0 {
			t.Errorf("il cookie di stato va cancellato: %+v", c)
		}
		// La sessione vale davvero: /auth/session risponde con alice e 'oidc'.
		cur := kcDo(h, http.MethodGet, "/auth/session", sess)
		var body struct {
			User       struct{ Username string }
			AuthMethod string
		}
		_ = json.Unmarshal(cur.Body.Bytes(), &body)
		if cur.Code != 200 || body.User.Username != alice.Username || body.AuthMethod != "oidc" {
			t.Errorf("sessione: %d %s", cur.Code, cur.Body.String())
		}
		if n := count(`SELECT count(*) FROM identity.oidc_identities WHERE user_id = $1`, alice.ID); n != 1 {
			t.Errorf("identità collegate = %d", n)
		}
	})

	t.Run("secondo login: via (provider, subject)", func(t *testing.T) {
		authURL, sc := kcStart(t, h, "")
		code, state := kcLogin(t, authURL, "alice", "alice-password")
		rec := kcCallback(h, code, state, sc)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
		}
		if n := count(`SELECT count(*) FROM identity.oidc_identities`); n != 1 {
			t.Errorf("identità = %d, deve restare 1", n)
		}
	})

	t.Run("email non verificata: 409 e nessun collegamento", func(t *testing.T) {
		authURL, sc := kcStart(t, h, "")
		code, state := kcLogin(t, authURL, "bob", "bob-password")
		rec := kcCallback(h, code, state, sc)
		if rec.Code != http.StatusConflict || kcErrCode(t, rec) != "oidc_identity_unlinked" {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
		}
		if cookieNamed(rec, auth.CookieName) != nil {
			t.Error("nessuna sessione doveva essere aperta")
		}
		if n := count(`SELECT count(*) FROM identity.oidc_identities i JOIN identity.users u ON u.id = i.user_id WHERE u.username = 'bob-locale'`); n != 0 {
			t.Error("bob-locale è stato collegato con un'email non verificata")
		}
		if n := count(`SELECT count(*) FROM identity.users WHERE username LIKE 'bob%'`); n != 1 {
			t.Errorf("utenti bob* = %d: con l'email già presa non si crea nulla", n)
		}
	})

	t.Run("autoCreateUsers: utente nuovo", func(t *testing.T) {
		authURL, sc := kcStart(t, h, "")
		code, state := kcLogin(t, authURL, "dave", "dave-password")
		rec := kcCallback(h, code, state, sc)
		if rec.Code != http.StatusFound {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
		}
		u, err := us.Get(ctx, "dave")
		if err != nil || u.Email == nil || *u.Email != "dave@example.com" || u.IsAdmin || u.DisplayName != "Dave Example" {
			t.Errorf("utente creato: %+v %v", u, err)
		}
	})

	t.Run("state e cookie: riuso, state diverso, senza cookie", func(t *testing.T) {
		authURL, sc := kcStart(t, h, "")
		code, state := kcLogin(t, authURL, "alice", "alice-password")
		if rec := kcCallback(h, code, state+"x", sc); rec.Code != http.StatusBadRequest || kcErrCode(t, rec) != "oidc_invalid_state" {
			t.Errorf("state diverso: %d %s", rec.Code, rec.Body.String())
		}
		// Lo state sbagliato non consuma il flusso: la callback giusta passa una volta sola.
		if rec := kcCallback(h, code, state, sc); rec.Code != http.StatusFound {
			t.Fatalf("callback giusta: %d %s", rec.Code, rec.Body.String())
		}
		if rec := kcCallback(h, code, state, sc); rec.Code != http.StatusBadRequest {
			t.Errorf("riuso di state e cookie: %d %s", rec.Code, rec.Body.String())
		}
		authURL, _ = kcStart(t, h, "")
		code, state = kcLogin(t, authURL, "alice", "alice-password")
		if rec := kcDo(h, http.MethodGet, "/auth/oidc/"+kcSlug+"/callback?code="+code+"&state="+state); rec.Code != http.StatusBadRequest {
			t.Errorf("senza cookie: %d", rec.Code)
		}
	})

	t.Run("start: provider sconosciuto e redirectTo non valido", func(t *testing.T) {
		if rec := kcDo(h, http.MethodGet, "/auth/oidc/altro/start"); rec.Code != http.StatusNotFound {
			t.Errorf("provider sconosciuto: %d", rec.Code)
		}
		if rec := kcDo(h, http.MethodGet, "/auth/oidc/"+kcSlug+"/start?redirectTo=https://evil.example/"); rec.Code != http.StatusBadRequest {
			t.Errorf("redirectTo assoluto: %d", rec.Code)
		}
		if rec := kcDo(h, http.MethodGet, "/auth/oidc/"+kcSlug+"/start?redirectTo=//evil.example"); rec.Code != http.StatusBadRequest {
			t.Errorf("redirectTo protocol-relative: %d", rec.Code)
		}
	})
}

// Con le opzioni di default (entrambe false) un'identità nuova non si collega
// né si crea, nemmeno con l'email verificata.
func TestKeycloak_DefaultIsUnlinked(t *testing.T) {
	h, us, count := kcSetup(t, false, false)
	if _, err := us.Create(context.Background(), users.CreateInput{Username: "alice-locale", Email: "alice@example.com", Password: pw}); err != nil {
		t.Fatal(err)
	}
	authURL, sc := kcStart(t, h, "")
	code, state := kcLogin(t, authURL, "alice", "alice-password")
	rec := kcCallback(h, code, state, sc)
	if rec.Code != http.StatusConflict || kcErrCode(t, rec) != "oidc_identity_unlinked" {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if n := count(`SELECT count(*) FROM identity.oidc_identities`); n != 0 {
		t.Errorf("identità = %d", n)
	}
}
