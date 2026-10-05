package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// downstream è un servizio a valle finto (core o identity): risponde 204 e
// registra le richieste che riceve.
type downstream struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func newDownstream(t *testing.T) *downstream {
	t.Helper()
	d := &downstream{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.seen = append(d.seen, r)
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *downstream) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

func (d *downstream) last() *http.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.seen) == 0 {
		return nil
	}
	return d.seen[len(d.seen)-1]
}

type env struct {
	router   http.Handler
	core     *downstream
	identity *downstream
	verifier *stubVerifier
	perms    *stubPermissions
}

func newEnv(t *testing.T, v *stubVerifier) *env {
	t.Helper()
	e := &env{core: newDownstream(t), identity: newDownstream(t), verifier: v, perms: &stubPermissions{}}
	cfg := newTestConfig(t, e.core.srv.URL)
	cfg.IdentityURL = mustURL(t, e.identity.srv.URL)
	cfg.IdentityTimeout = time.Second
	cfg.IdentityServiceSecret = testSecret
	e.router = NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(e.perms),
		WithClock(func() time.Time { return time.Unix(1700000000, 0) }))
	return e
}

func (e *env) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non è un errore del contratto: %q", rec.Body.String())
	}
	return body.Error.Code
}

func tokenResult(scopes ...string) identityclient.Result {
	return identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "22222222-2222-2222-2222-222222222222", Username: "bot", AuthMethod: "token", IsToken: true, Scopes: scopes,
	}}
}

func sessionResult(username string, mustChange bool) identityclient.Result {
	return identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "33333333-3333-3333-3333-333333333333", Username: username, AuthMethod: "password", MustChangePassword: mustChange,
	}}
}

func bearerReq(method, target, token string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func cookieReq(method, target, value string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: "gst_session", Value: value})
	return req
}

// Ogni operazione non interna del contratto ha una dichiarazione di
// sicurezza; ogni pattern di identityPatterns corrisponde a rotte
// dichiarate e ogni rotta dichiarata di identity è coperta da un pattern.
// Una rotta nuova nel contratto (o un pattern nuovo) senza dichiarazione fa
// fallire questo test.
func TestSicurezza_OgniRottaHaLaSuaDichiarazione(t *testing.T) {
	// La spec arriva dal modulo api (go run in modalità workspace), come in
	// contractRoutes.
	specFile := filepath.Join(t.TempDir(), "openapi.yaml")
	if out, err := exec.Command("go", "run", "github.com/fathorMB/GitStack/api/cmd/specdump", specFile).CombinedOutput(); err != nil {
		t.Fatalf("specdump: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	table, err := security.NewTable(security.Routes)
	if err != nil {
		t.Fatal(err)
	}

	param := regexp.MustCompile(`\{[^}]+\}`)
	httpMethods := map[string]string{"get": "GET", "put": "PUT", "post": "POST", "delete": "DELETE", "patch": "PATCH"}
	declared := 0
	for path, item := range doc.Paths {
		for key, node := range item {
			method, ok := httpMethods[key]
			if !ok {
				continue
			}
			var op struct {
				OperationID string   `yaml:"operationId"`
				Tags        []string `yaml:"tags"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatal(err)
			}
			m, found := table.Lookup(method, param.ReplaceAllString(path, "x"))
			if hasAny(op.Tags, map[string]bool{"internal": true, "git-internal": true}) {
				if found {
					t.Errorf("%s %s (internal) non deve avere una dichiarazione nel gateway", method, path)
				}
				continue
			}
			declared++
			if !found {
				t.Errorf("%s %s (%s): nessuna dichiarazione di sicurezza", method, path, op.OperationID)
				continue
			}
			if m.Route.OperationID != op.OperationID {
				t.Errorf("%s %s: dichiarazione di %s, attesa %s", method, path, m.Route.OperationID, op.OperationID)
			}
			if m.Route.Public && len(m.Route.Scopes) != 0 || !m.Route.Public && len(m.Route.Credentials) == 0 {
				t.Errorf("%s %s: dichiarazione incoerente %+v", method, path, m.Route)
			}
		}
	}
	if declared != len(security.Routes) {
		t.Errorf("il contratto ha %d operazioni non interne, la tabella %d", declared, len(security.Routes))
	}

	// identityPatterns <-> rotte dichiarate di identity.
	mux := http.NewServeMux()
	for _, p := range identityPatterns {
		mux.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	used := map[string]bool{}
	for _, r := range security.Routes {
		if r.Service != security.ServiceIdentity {
			continue
		}
		_, pattern := mux.Handler(httptest.NewRequest(r.Method, "/v1"+param.ReplaceAllString(r.Path, "x"), nil))
		if pattern == "" {
			t.Errorf("%s %s (%s) è dichiarata per identity ma nessun identityPatterns la instrada", r.Method, r.Path, r.OperationID)
		}
		used[pattern] = true
	}
	for _, p := range identityPatterns {
		if !used[p] {
			t.Errorf("identityPatterns %q non instrada nessuna rotta dichiarata: rotta senza dichiarazione di sicurezza", p)
		}
	}
}

// Ogni rotta dichiarata per core è servita dal router e arriva a core.
func TestSicurezza_RotteDiCoreServite(t *testing.T) {
	table, _ := security.NewTable(security.Routes)
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, r := range security.Routes {
		if r.Service != security.ServiceCore {
			continue
		}
		e := newEnv(t, allowAll())
		// ?path=x&q=xx: le letture di file hanno `path` obbligatorio nel contratto, la ricerca `q`.
		path := param.ReplaceAllStringFunc(r.Path, func(p string) string {
			if p == "{number}" || p == "{milestoneNumber}" {
				return "1" // numeri interi (issues, milestone), non UUID
			}
			return "00000000-0000-0000-0000-000000000000"
		})
		rec := e.do(authedRequestFor(table, r.Method, "/v1"+path+"?path=x&q=xx"))
		if rec.Code != http.StatusNoContent || e.core.count() != 1 {
			t.Errorf("%s %s: status %d, richieste a core %d", r.Method, r.Path, rec.Code, e.core.count())
		}
	}
}

func TestAuth_TabellaDiRotte(t *testing.T) {
	v := &stubVerifier{results: map[string]identityclient.Result{
		"gst_read_user":  tokenResult("read:user"),
		"gst_write_user": tokenResult("write:user"),
		"gst_admin_org":  tokenResult("admin:org"),
		"gst_resource":   tokenResult("read:resource"),
		"sess":           sessionResult("alice", false),
	}}
	cases := []struct {
		name       string
		req        func() *http.Request
		wantStatus int
		wantCode   string
		wantDown   string // "core", "identity" o "" se non deve arrivare a valle
	}{
		// rotte `security: []`: senza credenziali
		{"health", func() *http.Request { return httptest.NewRequest("GET", "/v1/health", nil) }, 204, "", "core"},
		{"login", func() *http.Request { return httptest.NewRequest("POST", "/v1/auth/login", nil) }, 204, "", "identity"},
		{"provider OIDC: elenco", func() *http.Request { return httptest.NewRequest("GET", "/v1/auth/oidc/providers", nil) }, 204, "", "identity"},
		{"provider OIDC: start", func() *http.Request { return httptest.NewRequest("GET", "/v1/auth/oidc/kc/start", nil) }, 204, "", "identity"},
		{"provider OIDC: callback", func() *http.Request { return httptest.NewRequest("GET", "/v1/auth/oidc/kc/callback?code=x", nil) }, 204, "", "identity"},
		{"login con credenziale non valida resta raggiungibile", func() *http.Request { return bearerReq("POST", "/v1/auth/login", "gst_scaduto") }, 204, "", "identity"},

		// senza credenziali
		{"resources senza credenziali", func() *http.Request { return httptest.NewRequest("GET", "/v1/resources", nil) }, 401, "unauthenticated", ""},
		{"crea resource senza credenziali", func() *http.Request { return httptest.NewRequest("POST", "/v1/resources", nil) }, 401, "unauthenticated", ""},
		{"users senza credenziali", func() *http.Request { return httptest.NewRequest("GET", "/v1/users", nil) }, 401, "unauthenticated", ""},
		{"sessione senza credenziali", func() *http.Request { return httptest.NewRequest("GET", "/v1/auth/session", nil) }, 401, "unauthenticated", ""},
		{"logout senza credenziali", func() *http.Request { return httptest.NewRequest("POST", "/v1/auth/logout", nil) }, 401, "unauthenticated", ""},
		{"Authorization non Bearer", func() *http.Request {
			r := httptest.NewRequest("GET", "/v1/users", nil)
			r.Header.Set("Authorization", "Basic YWxpY2U6cHc=")
			return r
		}, 401, "unauthenticated", ""},
		{"Bearer che non è un token gst_", func() *http.Request { return bearerReq("GET", "/v1/users", "abc") }, 401, "unauthenticated", ""},
		{"cookie vuoto", func() *http.Request { return cookieReq("GET", "/v1/users", "") }, 401, "unauthenticated", ""},

		// credenziale non attiva
		{"token non attivo", func() *http.Request { return bearerReq("GET", "/v1/resources", "gst_sconosciuto") }, 401, "unauthenticated", ""},
		{"sessione non attiva", func() *http.Request { return cookieReq("GET", "/v1/resources", "vecchia") }, 401, "unauthenticated", ""},

		// scope
		{"token con lo scope giusto", func() *http.Request { return bearerReq("GET", "/v1/users", "gst_read_user") }, 204, "", "identity"},
		{"write include read", func() *http.Request { return bearerReq("GET", "/v1/users", "gst_write_user") }, 204, "", "identity"},
		{"read non basta per write", func() *http.Request { return bearerReq("POST", "/v1/users", "gst_read_user") }, 403, "insufficient_scope", ""},
		{"scope di un altro ambito", func() *http.Request { return bearerReq("GET", "/v1/orgs", "gst_read_user") }, 403, "insufficient_scope", ""},
		{"admin:org include write:org", func() *http.Request { return bearerReq("PATCH", "/v1/orgs/acme", "gst_admin_org") }, 204, "", "identity"},
		{"admin:org elimina l'org", func() *http.Request { return bearerReq("DELETE", "/v1/orgs/acme", "gst_admin_org") }, 204, "", "identity"},
		{"write:user non basta per admin:org", func() *http.Request { return bearerReq("DELETE", "/v1/orgs/acme", "gst_write_user") }, 403, "insufficient_scope", ""},
		{"token senza scope sulla sessione corrente", func() *http.Request { return bearerReq("GET", "/v1/auth/session", "gst_resource") }, 204, "", "identity"},
		{"resources con token qualunque autenticato", func() *http.Request { return bearerReq("GET", "/v1/resources", "gst_resource") }, 204, "", "core"},

		// sessioni: senza scope, passano
		{"sessione su users", func() *http.Request { return cookieReq("GET", "/v1/users", "sess") }, 204, "", "identity"},
		{"sessione su org admin", func() *http.Request { return cookieReq("DELETE", "/v1/orgs/acme", "sess") }, 204, "", "identity"},
		{"sessione su resources", func() *http.Request { return cookieReq("GET", "/v1/resources", "sess") }, 204, "", "core"},
		{"logout con sessione", func() *http.Request { return cookieReq("POST", "/v1/auth/logout", "sess") }, 204, "", "identity"},
		{"logout con un token: solo sessione", func() *http.Request { return bearerReq("POST", "/v1/auth/logout", "gst_read_user") }, 401, "unauthenticated", ""},

		// mai esposto / non dichiarato
		{"internal", func() *http.Request { return bearerReq("POST", "/v1/internal/verify", "gst_admin_org") }, 404, "", ""},
		{"metodo non dichiarato", func() *http.Request { return bearerReq("PATCH", "/v1/users", "gst_admin_org") }, 404, "", ""},
		{"percorso sotto un pattern ma non dichiarato", func() *http.Request { return bearerReq("GET", "/v1/auth/inventata", "gst_admin_org") }, 404, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, v)
			rec := e.do(c.req())
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, voluto %d (corpo %s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantCode != "" {
				if got := errorCode(t, rec); got != c.wantCode {
					t.Errorf("code = %q, voluto %q", got, c.wantCode)
				}
			}
			core, ident := e.core.count(), e.identity.count()
			switch c.wantDown {
			case "core":
				if core != 1 || ident != 0 {
					t.Errorf("a valle: core %d identity %d, atteso solo core", core, ident)
				}
			case "identity":
				if core != 0 || ident != 1 {
					t.Errorf("a valle: core %d identity %d, atteso solo identity", core, ident)
				}
			default:
				if core+ident != 0 {
					t.Errorf("la richiesta è arrivata a valle (core %d identity %d)", core, ident)
				}
			}
		})
	}
}

func TestAuth_InsufficientScopeDichiaraGliScopeRichiesti(t *testing.T) {
	e := newEnv(t, &stubVerifier{results: map[string]identityclient.Result{"gst_r": tokenResult("read:user")}})
	rec := e.do(bearerReq("POST", "/v1/orgs", "gst_r"))
	var body struct {
		Error struct {
			Details struct {
				Required []string `json:"required"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 403 {
		t.Fatalf("status %d corpo %s", rec.Code, rec.Body.String())
	}
	if len(body.Error.Details.Required) != 1 || body.Error.Details.Required[0] != "write:org" {
		t.Errorf("details.required = %v", body.Error.Details.Required)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// Se identity è giù (o non risponde come deve) le rotte autenticate danno
// 503, mai fail open; le rotte pubbliche restano raggiungibili.
func TestAuth_IdentityGiuNonAprePerErrore(t *testing.T) {
	for _, v := range []*stubVerifier{{err: identityclient.ErrUnavailable}, nil} {
		var e *env
		if v == nil {
			// Nessun verificatore configurato (senza GITSTACK_IDENTITY_URL).
			core := newDownstream(t)
			e = &env{core: core, identity: newDownstream(t)}
			e.router = NewRouter(newTestConfig(t, core.srv.URL), discardLogger())
		} else {
			e = newEnv(t, v)
		}
		for _, req := range []*http.Request{bearerReq("GET", "/v1/resources", "gst_x"), cookieReq("GET", "/v1/resources", "x")} {
			rec := e.do(req)
			if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "identity_unavailable" {
				t.Errorf("status %d corpo %s, atteso 503 identity_unavailable", rec.Code, rec.Body.String())
			}
			if e.core.count() != 0 {
				t.Error("la richiesta è arrivata a core con identity giù")
			}
		}
		if rec := e.do(httptest.NewRequest("GET", "/v1/health", nil)); rec.Code != http.StatusNoContent {
			t.Errorf("/v1/health con identity giù: status %d, deve restare raggiungibile", rec.Code)
		}
	}
}

// Sessione con password iniziale da cambiare: 403 password_change_required
// ovunque, tranne GET /auth/session, POST /auth/logout e PUT della propria
// password.
func TestAuth_PasswordDaCambiare(t *testing.T) {
	v := &stubVerifier{results: map[string]identityclient.Result{
		"admin-sess": sessionResult("admin", true),
		"ok-sess":    sessionResult("admin", false),
	}}
	cases := []struct {
		name, method, path string
		want               int
		wantDown           string
	}{
		{"GET /auth/session", "GET", "/v1/auth/session", 204, "identity"},
		{"POST /auth/logout", "POST", "/v1/auth/logout", 204, "identity"},
		{"PUT della propria password", "PUT", "/v1/users/admin/password", 204, "identity"},
		{"PUT della password di un altro", "PUT", "/v1/users/bob/password", 403, ""},
		{"GET /users", "GET", "/v1/users", 403, ""},
		{"GET /users/admin", "GET", "/v1/users/admin", 403, ""},
		{"POST /users", "POST", "/v1/users", 403, ""},
		{"GET /resources", "GET", "/v1/resources", 403, ""},
		{"POST /resources", "POST", "/v1/resources", 403, ""},
		{"GET /orgs", "GET", "/v1/orgs", 403, ""},
		{"GET /user/tokens", "GET", "/v1/user/tokens", 403, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, v)
			rec := e.do(cookieReq(c.method, c.path, "admin-sess"))
			if rec.Code != c.want {
				t.Fatalf("status = %d, voluto %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			if c.want == 403 && errorCode(t, rec) != "password_change_required" {
				t.Errorf("code = %s", rec.Body.String())
			}
			if (c.wantDown == "identity") != (e.identity.count() == 1) || e.core.count() != 0 {
				t.Errorf("a valle: identity %d core %d, atteso %q", e.identity.count(), e.core.count(), c.wantDown)
			}
		})
	}

	// Dopo il cambio la stessa sessione passa ovunque.
	e := newEnv(t, v)
	if rec := e.do(cookieReq("GET", "/v1/users", "ok-sess")); rec.Code != http.StatusNoContent {
		t.Errorf("sessione con password cambiata: status %d", rec.Code)
	}
}

// Il gateway cancella gli header X-Gitstack-* del client e scrive i propri,
// firmati: core può verificare che vengono dal gateway.
func TestAuth_IdentitaFirmataVersoIServiziAValle(t *testing.T) {
	v := &stubVerifier{results: map[string]identityclient.Result{
		"gst_tok":  tokenResult("write:user", "read:org", "read:resource"),
		"sess":     sessionResult("alice", false),
		"gst_evil": tokenResult("read:resource"),
	}}
	forge := func(r *http.Request) *http.Request {
		r.Header.Set("X-Gitstack-User-Id", "99999999-9999-9999-9999-999999999999")
		r.Header.Set("X-Gitstack-Username", "root")
		r.Header.Set("X-Gitstack-Scopes", "admin:org")
		r.Header.Set("X-Gitstack-Signature", "abcd")
		r.Header.Set("x-gitstack-timestamp", "1")
		r.Header.Set("X-Gitstack-Qualsiasi", "x")
		return r
	}
	expect := func(t *testing.T, got *http.Request, id trust.Identity) {
		t.Helper()
		want := http.Header{}
		trust.Sign(want, testSecret, id, time.Unix(1700000000, 0))
		for _, h := range []string{trust.HeaderUserID, trust.HeaderUsername, trust.HeaderScopes, trust.HeaderTimestamp, trust.HeaderSignature} {
			if got.Header.Get(h) != want.Get(h) || len(got.Header.Values(h)) != 1 {
				t.Errorf("%s = %v, voluto %q", h, got.Header.Values(h), want.Get(h))
			}
		}
		if got.Header.Get("X-Gitstack-Qualsiasi") != "" {
			t.Error("un header X-Gitstack-* del client è arrivato a valle")
		}
	}

	t.Run("token verso core", func(t *testing.T) {
		e := newEnv(t, v)
		req := forge(bearerReq("GET", "/v1/resources", "gst_tok"))
		req.Header.Set("Cookie", "gst_session=sess")
		if rec := e.do(req); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		got := e.core.last()
		expect(t, got, trust.Identity{UserID: "22222222-2222-2222-2222-222222222222", Username: "bot", Scopes: []string{"write:user", "read:org", "read:resource"}})
		// core non vede mai la credenziale del client.
		if got.Header.Get("Authorization") != "" || got.Header.Get("Cookie") != "" {
			t.Errorf("credenziali arrivate a core: Authorization=%q Cookie=%q", got.Header.Get("Authorization"), got.Header.Get("Cookie"))
		}
	})
	t.Run("sessione verso core: scope vuoti", func(t *testing.T) {
		e := newEnv(t, v)
		if rec := e.do(forge(cookieReq("GET", "/v1/resources", "sess"))); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		expect(t, e.core.last(), trust.Identity{UserID: "33333333-3333-3333-3333-333333333333", Username: "alice"})
	})
	t.Run("verso identity l'identità è firmata e le credenziali passano", func(t *testing.T) {
		e := newEnv(t, v)
		if rec := e.do(forge(cookieReq("GET", "/v1/auth/session", "sess"))); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		got := e.identity.last()
		expect(t, got, trust.Identity{UserID: "33333333-3333-3333-3333-333333333333", Username: "alice"})
		if c, err := got.Cookie("gst_session"); err != nil || c.Value != "sess" {
			t.Errorf("identity deve ricevere il cookie di sessione: %v", err)
		}
	})
	t.Run("rotta pubblica: nessun header d'identità, quelli del client eliminati", func(t *testing.T) {
		e := newEnv(t, v)
		if rec := e.do(forge(httptest.NewRequest("GET", "/v1/health", nil))); rec.Code != 204 {
			t.Fatal(rec.Code)
		}
		for name := range e.core.last().Header {
			if strings.HasPrefix(name, "X-Gitstack-") && name != "X-Gitstack-Client-Ip" {
				t.Errorf("header %s arrivato a core su una rotta pubblica", name)
			}
		}
		if e.core.last().Header.Get(trust.HeaderSignature) != "" {
			t.Error("firma del client arrivata a core")
		}
	})
}

// Con il client e la cache reali (fake identity HTTP): la cache tiene 30 s,
// il cambio password toglie la voce (mustChangePassword cambia subito), e con
// identity giù la voce scaduta non viene riusata.
func TestAuth_CacheEChiamataAIdentity(t *testing.T) {
	var verifyCalls atomic.Int32
	var mustChange atomic.Bool
	var down atomic.Bool
	mustChange.Store(true)
	var gotSecret atomic.Value
	fakeIdentity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/verify" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if down.Load() {
			http.Error(w, "giù", http.StatusBadGateway)
			return
		}
		verifyCalls.Add(1)
		gotSecret.Store(r.Header.Get("Authorization"))
		var in struct{ Credential string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Credential != "sess" {
			_, _ = w.Write([]byte(`{"active":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"33333333-3333-3333-3333-333333333333","username":"admin","kind":"human","isAdmin":true,"authMethod":"password","mustChangePassword":` + map[bool]string{true: "true", false: "false"}[mustChange.Load()] + `}}`))
	}))
	defer fakeIdentity.Close()
	core := newDownstream(t)

	cfg := newTestConfig(t, core.srv.URL)
	cfg.IdentityURL = mustURL(t, fakeIdentity.URL)
	cfg.IdentityTimeout = time.Second
	cfg.IdentityServiceSecret = testSecret
	cfg.AuthCacheTTL = 30 * time.Second
	cfg.AuthCacheNegativeTTL = 5 * time.Second
	now := time.Unix(1700000000, 0)
	var nowMu sync.Mutex
	clock := func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }
	advance := func(d time.Duration) { nowMu.Lock(); now = now.Add(d); nowMu.Unlock() }
	router := NewRouter(cfg, discardLogger(), WithClock(clock))

	do := func(req *http.Request) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := do(cookieReq("GET", "/v1/users", "sess")); got != 403 {
		t.Fatalf("GET /users con password da cambiare: %d", got)
	}
	if gotSecret.Load() != "Bearer "+testSecret {
		t.Errorf("il gateway deve chiamare /internal/verify con il segreto di servizio, ha mandato %v", gotSecret.Load())
	}
	do(cookieReq("GET", "/v1/users", "sess"))
	do(cookieReq("GET", "/v1/auth/session", "sess"))
	if verifyCalls.Load() != 1 {
		t.Fatalf("chiamate a /internal/verify = %d, volevo 1 (cache)", verifyCalls.Load())
	}

	// Cambio password: la voce in cache si toglie, il gateway rilegge da
	// identity e la stessa sessione non è più bloccata.
	mustChange.Store(false)
	if got := do(cookieReq("PUT", "/v1/users/admin/password", "sess")); got != 204 {
		t.Fatalf("PUT della propria password: %d", got)
	}
	if got := do(cookieReq("GET", "/v1/resources", "sess")); got != 204 {
		t.Fatalf("GET /resources dopo il cambio password: %d, voluto 204 subito (voce di cache tolta)", got)
	}
	calls := verifyCalls.Load()
	if calls != 2 {
		t.Fatalf("chiamate a /internal/verify = %d, volevo 2", calls)
	}

	// Esito negativo: 5 s.
	if got := do(cookieReq("GET", "/v1/resources", "inventata")); got != 401 {
		t.Fatalf("credenziale inventata: %d", got)
	}
	do(cookieReq("GET", "/v1/resources", "inventata"))
	if verifyCalls.Load() != calls+1 {
		t.Fatalf("l'esito negativo va in cache: chiamate = %d", verifyCalls.Load()-calls)
	}

	// Identity giù: nei 30 s si serve dalla cache, poi 503 (senza allungare).
	down.Store(true)
	advance(20 * time.Second)
	if got := do(cookieReq("GET", "/v1/resources", "sess")); got != 204 {
		t.Fatalf("dentro il TTL con identity giù: %d, voluto 204 dalla cache", got)
	}
	advance(11 * time.Second)
	if got := do(cookieReq("GET", "/v1/resources", "sess")); got != 503 {
		t.Fatalf("oltre il TTL con identity giù: %d, voluto 503", got)
	}
	if got := do(cookieReq("GET", "/v1/resources", "inventata")); got != 503 {
		t.Fatalf("credenziale inventata oltre il TTL negativo con identity giù: %d, voluto 503 (mai fail open né 401 dalla cache scaduta)", got)
	}
	down.Store(false)
	if got := do(cookieReq("GET", "/v1/resources", "sess")); got != 204 {
		t.Fatalf("identity di nuovo su: %d", got)
	}
}

func TestNewRouter_ConfigSenzaSegretoNonProduceIdentitaFirmata(t *testing.T) {
	// Senza segreto (identity non configurata) nessuna rotta autenticata passa.
	core := newDownstream(t)
	cfg := config.Config{CoreURL: mustURL(t, core.srv.URL), CoreTimeout: time.Second}
	router := NewRouter(cfg, discardLogger())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, bearerReq("GET", "/v1/resources", "gst_x"))
	if rec.Code != 503 || core.count() != 0 {
		t.Fatalf("status %d, richieste a core %d", rec.Code, core.count())
	}
}

// Una credenziale oltre i 512 caratteri (maxLength di
// VerifyCredentialInput.credential) è non valida: 401 senza interrogare
// identity, che risponderebbe 400 e farebbe scambiare la richiesta per
// un'indisponibilità.
func TestAuth_CredenzialeTroppoLunga(t *testing.T) {
	for name, req := range map[string]*http.Request{
		"bearer 516": bearerReq("GET", "/v1/resources", "gst_"+strings.Repeat("A", 512)),
		"cookie 513": cookieReq("GET", "/v1/resources", strings.Repeat("A", 513)),
	} {
		v := allowAll()
		e := newEnv(t, v)
		rec := e.do(req)
		if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "unauthenticated" {
			t.Errorf("%s: status %d corpo %s, atteso 401 unauthenticated", name, rec.Code, rec.Body.String())
		}
		if v.calls != 0 {
			t.Errorf("%s: il Verifier è stato chiamato %d volte", name, v.calls)
		}
		if e.core.count() != 0 {
			t.Errorf("%s: la richiesta è arrivata a core", name)
		}
	}
}

// Il limite è inclusivo: 512 caratteri esatti arrivano al Verifier.
func TestAuth_CredenzialeDi512CaratteriArrivaAlVerifier(t *testing.T) {
	v := &stubVerifier{}
	e := newEnv(t, v)
	rec := e.do(bearerReq("GET", "/v1/resources", "gst_"+strings.Repeat("A", 508)))
	if v.calls != 1 {
		t.Fatalf("chiamate al Verifier = %d, volevo 1", v.calls)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, atteso 401 (credenziale non attiva)", rec.Code)
	}
}
