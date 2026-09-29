//go:build integration

// Package stackitest prova insieme gateway, identity e core: login,
// chiamata alla risorsa di prova di core (/api/v1/resources) con cookie e
// con token, 401 senza credenziali, identità che core accetta solo dal
// gateway.
//
// Identity e gateway sono i binari veri (compilati qui con `go build` dalle
// loro cartelle, configurati con le stesse variabili d'ambiente del chart);
// core è il router vero in-process, sullo stesso Postgres. Serve
// GITSTACK_TEST_DATABASE_URL (vedi internal/dbtest); senza, il test è
// saltato.
package stackitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

const (
	serviceSecret = "segreto-di-servizio-di-integrazione"
	adminPassword = "password-iniziale-dell-admin-1234"
	newPassword   = "nuova password molto lunga 42"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func build(t *testing.T, dir, out string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), out)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", dir, err, outb)
	}
	return bin
}

// start avvia un binario e lo ferma (per PID) a fine test.
func start(t *testing.T, name, bin, addr string, env ...string) {
	t.Helper()
	var logs bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("log di %s:\n%s", name, logs.String())
		}
		for _, secret := range []string{serviceSecret, adminPassword, newPassword} {
			if strings.Contains(logs.String(), secret) {
				t.Errorf("un segreto compare nei log di %s", name)
			}
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s non è partito in tempo:\n%s", name, logs.String())
}

type reply struct {
	status  int
	body    []byte
	cookies []*http.Cookie
}

func (r reply) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (r reply) errCode() string {
	e, _ := r.json()["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

type stack struct {
	t       *testing.T
	gateway string
	core    string
}

func (s *stack) do(method, url string, body any, hdr map[string]string, cookie *http.Cookie) reply {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b, cookies: resp.Cookies()}
}

func (s *stack) gw(method, path string, body any, hdr map[string]string, cookie *http.Cookie) reply {
	s.t.Helper()
	return s.do(method, s.gateway+"/v1"+path, body, hdr, cookie)
}

func want(t *testing.T, r reply, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.errCode() != code) {
		t.Fatalf("status %d code %q (corpo %s); atteso %d %q", r.status, r.errCode(), r.body, status, code)
	}
}

func TestGatewayIdentityCore(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)

	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")

	// core: il router vero, in-process, sullo stesso database.
	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret))
	t.Cleanup(coreSrv.Close)

	identityAddr, gatewayAddr := freeAddr(t), freeAddr(t)
	start(t, "identity", identityBin, identityAddr,
		"GITSTACK_IDENTITY_ADDR="+identityAddr,
		"GITSTACK_IDENTITY_DB_URL="+dsn,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_IDENTITY_ADMIN_USERNAME=admin",
		"GITSTACK_IDENTITY_ADMIN_PASSWORD="+adminPassword,
	)
	start(t, "gateway", gatewayBin, gatewayAddr,
		"GITSTACK_GATEWAY_ADDR="+gatewayAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		// TTL breve, per provare la scadenza dopo il logout senza attese lunghe.
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	// --- senza credenziali: 401 ---------------------------------------------
	want(t, s.gw("GET", "/resources", nil, nil, nil), 401, "unauthenticated")
	want(t, s.gw("POST", "/resources", map[string]any{"type": "repo", "name": "x"}, nil, nil), 401, "unauthenticated")
	want(t, s.gw("GET", "/auth/session", nil, nil, nil), 401, "unauthenticated")
	want(t, s.gw("GET", "/resources", nil, map[string]string{"Authorization": "Bearer gst_inventato"}, nil), 401, "unauthenticated")
	want(t, s.gw("GET", "/resources", nil, nil, &http.Cookie{Name: "gst_session", Value: "inventato"}), 401, "unauthenticated")
	// rotte pubbliche raggiungibili senza credenziali
	want(t, s.gw("GET", "/health", nil, nil, nil), 200, "")
	want(t, s.gw("GET", "/auth/oidc/providers", nil, nil, nil), 200, "")
	want(t, s.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": "sbagliata sbagliata"}, nil, nil), 401, "invalid_credentials")
	// /internal non è mai esposto
	want(t, s.gw("POST", "/internal/verify", map[string]any{"credential": "x"}, nil, nil), 404, "")

	// --- login dell'admin: password iniziale da cambiare --------------------
	login := s.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, nil, nil)
	want(t, login, 200, "")
	var cookie *http.Cookie
	for _, c := range login.cookies {
		if c.Name == "gst_session" {
			cookie = c
		}
	}
	if cookie == nil || login.json()["mustChangePassword"] != true {
		t.Fatalf("login admin: %s", login.body)
	}
	want(t, s.gw("GET", "/resources", nil, nil, cookie), 403, "password_change_required")
	want(t, s.gw("GET", "/users", nil, nil, cookie), 403, "password_change_required")
	want(t, s.gw("GET", "/auth/session", nil, nil, cookie), 200, "")
	want(t, s.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, cookie), 204, "")

	// --- con il cookie: sbloccato subito, anche dentro il TTL della cache ----
	want(t, s.gw("GET", "/resources", nil, nil, cookie), 200, "")
	created := s.gw("POST", "/resources", map[string]any{"type": "repo", "name": "gitstack"}, nil, cookie)
	want(t, created, 201, "")
	id, _ := created.json()["id"].(string)
	want(t, s.gw("GET", "/resources/"+id, nil, nil, cookie), 200, "")
	want(t, s.gw("GET", "/users", nil, nil, cookie), 200, "")

	// --- con un token con scope ---------------------------------------------
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	tok := s.gw("POST", "/user/tokens", map[string]any{"name": "ci", "scopes": []string{"read:user"}, "expiresAt": exp}, nil, cookie)
	want(t, tok, 201, "")
	token, _ := tok.json()["token"].(string)
	if !strings.HasPrefix(token, "gst_") {
		t.Fatalf("token: %s", tok.body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + token}
	want(t, s.gw("GET", "/resources", nil, bearer, nil), 200, "")
	want(t, s.gw("GET", "/resources/"+id, nil, bearer, nil), 200, "")
	// Le rotte di identity con un token: il gateway le lascia passare (scope
	// ok), ma i gestori di identity autenticano ancora solo il cookie di
	// sessione (vedi il README del gateway, "Limiti"): non si asserisce nulla.
	// scope insufficiente: 403 dal gateway, senza arrivare a identity
	r := s.gw("POST", "/orgs", map[string]any{"name": "acme"}, bearer, nil)
	want(t, r, 403, "insufficient_scope")
	if e, _ := r.json()["error"].(map[string]any); fmt.Sprint(e["details"]) != "map[required:[write:org]]" {
		t.Errorf("details = %v", e["details"])
	}
	want(t, s.gw("POST", "/users", map[string]any{"username": "x"}, bearer, nil), 403, "insufficient_scope")
	// il logout accetta solo la sessione
	want(t, s.gw("POST", "/auth/logout", nil, bearer, nil), 401, "unauthenticated")

	// --- header X-Gitstack-* del client: ignorati, e core li rifiuta se diretti
	forged := map[string]string{
		"X-Gitstack-User-Id": "99999999-9999-9999-9999-999999999999", "X-Gitstack-Username": "root", "X-Gitstack-Scopes": "admin:org",
	}
	want(t, s.gw("GET", "/resources", nil, forged, nil), 401, "unauthenticated")
	// core direttamente: senza identità, con header inventati o firmati male
	want(t, s.do("GET", s.core+"/resources", nil, nil, nil), 401, "unauthenticated")
	want(t, s.do("GET", s.core+"/resources", nil, forged, nil), 401, "unauthenticated")
	bad := map[string]string{}
	h := http.Header{}
	trust.Sign(h, "un-altro-segreto", trust.Identity{UserID: "99999999-9999-9999-9999-999999999999", Username: "root"}, time.Now())
	for k := range h {
		bad[k] = h.Get(k)
	}
	want(t, s.do("GET", s.core+"/resources", nil, bad, nil), 401, "unauthenticated")
	// firmato con il segreto giusto (cioè: da chi conosce il segreto di servizio)
	good := http.Header{}
	trust.Sign(good, serviceSecret, trust.Identity{UserID: "99999999-9999-9999-9999-999999999999", Username: "root"}, time.Now())
	goodHdr := map[string]string{}
	for k := range good {
		goodHdr[k] = good.Get(k)
	}
	want(t, s.do("GET", s.core+"/resources", nil, goodHdr, nil), 200, "")

	// --- logout: la sessione smette di valere entro il TTL della cache --------
	want(t, s.gw("POST", "/auth/logout", nil, nil, cookie), 204, "")
	time.Sleep(1200 * time.Millisecond)
	want(t, s.gw("GET", "/resources", nil, nil, cookie), 401, "unauthenticated")
	// il token, revocabile ma non revocato, vale ancora
	want(t, s.gw("GET", "/resources", nil, bearer, nil), 200, "")

}
