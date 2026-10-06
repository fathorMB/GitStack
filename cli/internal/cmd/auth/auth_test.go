package auth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
)

const secret = "gst_SEGRETO_che_non_deve_comparire"

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", errors.New("no") }

type result struct {
	code        int
	out, errOut string
}

type env struct {
	t       *testing.T
	dir     string
	vars    map[string]string
	stdin   string
	inTTY   bool
	client  func() *http.Client
	opened  []string
	gitArgs [][]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	keyring.MockInit()
	return &env{t: t, dir: filepath.Join(t.TempDir(), "gs"), vars: map[string]string{}}
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	ios, in, out, errOut := cmdutil.Test()
	in.WriteString(e.stdin)
	ios.InTTY = e.inTTY
	f := cmdutil.New("dev", ios)
	f.Getenv = func(k string) string {
		if k == config.EnvConfigDir {
			return e.dir
		}
		return e.vars[k]
	}
	f.Git = noGit{}
	if e.client != nil {
		f.HTTPClient = e.client
	}
	f.OpenBrowser = func(u string) error { e.opened = append(e.opened, u); return nil }
	f.RunGit = func(_ context.Context, a ...string) error { e.gitArgs = append(e.gitArgs, a); return nil }
	f.Executable = func() (string, error) { return "/usr/local/bin/gs", nil }
	code := root.Run(context.Background(), f, args)
	return result{code, out.String(), errOut.String()}
}

// server finge un'istanza: sessione per i token in tokens (valore: utente);
// gli altri 401. Gli scope sono sempre read:user e read:resource.
func server(t *testing.T, tokens map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/meta") {
			w.WriteHeader(404)
			return
		}
		user, ok := tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"token non valido"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authMethod":"token","mustChangePassword":false,"expiresAt":"2030-01-02T03:04:05Z","scopes":["read:user","read:resource"],"user":{"username":"`+user+`","displayName":"X","kind":"user"}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hostOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func (e *env) hostFile(host string) string {
	return filepath.Join(e.dir, "hosts", strings.NewReplacer(":", "_", "/", "_").Replace(host)+".yaml")
}

func noSecret(t *testing.T, rs ...result) {
	t.Helper()
	for _, r := range rs {
		if strings.Contains(r.out, secret) || strings.Contains(r.errOut, secret) {
			t.Errorf("il token è comparso nell'output: %+v", r)
		}
	}
}

func TestLoginWithTokenPortachiavi(t *testing.T) {
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	e.client = srv.Client
	e.stdin = secret + "\n"
	r := e.run("auth", "login", "--hostname", srv.URL, "--with-token")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecret(t, r)
	if !strings.Contains(r.errOut, "alice") || !strings.Contains(r.errOut, "portachiavi") {
		t.Errorf("messaggio: %q", r.errOut)
	}
	got, err := keyring.Get(config.KeyringService, srv.URL)
	if err != nil || got != secret {
		t.Fatalf("portachiavi: %q %v", got, err)
	}
	b, err := os.ReadFile(e.hostFile(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) || !strings.Contains(string(b), "user: alice") {
		t.Errorf("file host:\n%s", b)
	}
}

func TestLoginInterattivoNascostoSenzaEco(t *testing.T) {
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	e.client = srv.Client
	e.inTTY = true
	e.stdin = srv.URL + "\n" + secret + "\n" // istanza, poi token
	r := e.run("auth", "login")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecret(t, r)
	if !strings.Contains(r.errOut, "Token:") {
		t.Errorf("prompt: %q", r.errOut)
	}
	if tok, err := keyring.Get(config.KeyringService, srv.URL); err != nil || tok != secret {
		t.Errorf("portachiavi: %v", err)
	}
}

func TestLoginSenzaTerminaleRichiedeWithToken(t *testing.T) {
	e := newEnv(t)
	if r := e.run("auth", "login", "--hostname", "git.test"); r.code != 2 {
		t.Errorf("exit %d (%q)", r.code, r.errOut)
	}
	if r := e.run("auth", "login"); r.code != 2 {
		t.Errorf("senza istanza: exit %d (%q)", r.code, r.errOut)
	}
}

func TestLoginWebApreLaPaginaDegliScope(t *testing.T) {
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	e.client = srv.Client
	e.inTTY = true
	e.stdin = secret + "\n"
	r := e.run("auth", "login", "--hostname", srv.URL, "--web")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(e.opened) != 1 {
		t.Fatalf("browser aperto %d volte", len(e.opened))
	}
	want := srv.URL + "/settings/tokens/new?expires=90&name=gs&scopes=read:user,read:org,read:resource,write:resource"
	if e.opened[0] != want {
		t.Errorf("URL:\n got %s\nwant %s", e.opened[0], want)
	}
	noSecret(t, r)
}

func TestLoginTokenRifiutatoNonSalvaNiente(t *testing.T) {
	srv := server(t, map[string]string{"altro": "bob"})
	e := newEnv(t)
	e.client = srv.Client
	e.stdin = secret
	r := e.run("auth", "login", "--hostname", srv.URL, "--with-token")
	if r.code != 4 {
		t.Fatalf("exit %d (%q)", r.code, r.errOut)
	}
	noSecret(t, r)
	if _, err := keyring.Get(config.KeyringService, srv.URL); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("portachiavi: %v", err)
	}
	if _, err := os.Stat(e.hostFile(srv.URL)); err == nil {
		t.Error("configurazione scritta per un token rifiutato")
	}
}

func TestLoginSenzaPortachiaviUsaIlFileSoloUtente(t *testing.T) {
	keyring.MockInitWithError(errors.New("nessun Secret Service"))
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	keyring.MockInitWithError(errors.New("nessun Secret Service"))
	e.client = srv.Client
	e.stdin = secret
	r := e.run("auth", "login", "--hostname", srv.URL, "--with-token")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	noSecret(t, r)
	if !strings.Contains(r.errOut, "file di configurazione") {
		t.Errorf("messaggio: %q", r.errOut)
	}
	p := e.hostFile(srv.URL)
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), secret) {
		t.Fatalf("token non nel file: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Errorf("permessi %v, attesi 0600", st.Mode().Perm())
		}
	}
	// E si rilegge con lo stesso TokenSource.
	e2 := e
	e2.vars["GS_HOST"] = srv.URL
	if r := e2.run("api", "user"); r.code != 0 {
		t.Errorf("api user con token da file: %+v", r)
	}
}

func TestGSTokenEGSHostSenzaConfigurazione(t *testing.T) {
	srv := server(t, map[string]string{secret: "ci-bot"})
	e := newEnv(t)
	e.client = srv.Client
	e.vars["GS_HOST"] = srv.URL
	e.vars["GS_TOKEN"] = secret
	if r := e.run("api", "user"); r.code != 0 || !strings.Contains(r.out, "ci-bot") {
		t.Fatalf("api user: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "hosts")); err == nil {
		t.Error("creata configurazione degli host")
	}
	// Ha la precedenza su un token salvato.
	if err := keyring.Set(config.KeyringService, srv.URL, "vecchio"); err != nil {
		t.Fatal(err)
	}
	if r := e.run("api", "user"); r.code != 0 || !strings.Contains(r.out, "ci-bot") {
		t.Fatalf("precedenza: %+v", r)
	}
	// status senza configurazione.
	r := e.run("auth", "status")
	if r.code != 0 || !strings.Contains(r.out, "ci-bot") || !strings.Contains(r.out, "GS_TOKEN") {
		t.Errorf("status: %+v", r)
	}
	noSecret(t, r)
}

func TestStatusPiuIstanzeEScaduto(t *testing.T) {
	a := server(t, map[string]string{"tok-a": "alice"})
	b := server(t, map[string]string{"nessuno": "x"}) // il token salvato è revocato
	e := newEnv(t)
	e.client = func() *http.Client { return http.DefaultClient }
	for host, tok := range map[string]string{a.URL: "tok-a", b.URL: "tok-b"} {
		if err := (config.KeyringTokens{Cfg: config.New(e.dir)}).SetToken(host, tok); err != nil {
			t.Fatal(err)
		}
		if err := config.New(e.dir).SaveHost(host, config.HostConfig{User: "u"}); err != nil {
			t.Fatal(err)
		}
	}

	r := e.run("auth", "status", "--hostname", a.URL)
	if r.code != 0 || !strings.Contains(r.out, "alice") || !strings.Contains(r.out, "read:user,read:resource") || !strings.Contains(r.out, "2030-01-0") {
		t.Fatalf("una istanza: %+v", r)
	}
	r = e.run("auth", "status")
	if r.code != 4 {
		t.Fatalf("token revocato: exit %d\n%s%s", r.code, r.out, r.errOut)
	}
	for _, want := range []string{a.URL, b.URL, "alice", "ok", "invalid"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("manca %q:\n%s", want, r.out)
		}
	}
	// JSON: stessi dati, mai il token.
	r = e.run("auth", "status", "--json", "host,user,scopes,status")
	if r.code != 4 || !strings.Contains(r.out, `"status": "invalid"`) && !strings.Contains(r.out, `"status":"invalid"`) {
		t.Errorf("json: %+v", r)
	}
	if strings.Contains(r.out, "tok-a") || strings.Contains(r.out, "tok-b") {
		t.Error("token nell'output JSON")
	}
}

func TestStatusGSTokenNonVaAdAltreIstanze(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+secret {
			leaked = true
		}
		w.WriteHeader(401)
	}))
	defer other.Close()
	mine := server(t, map[string]string{secret: "ci-bot"})
	e := newEnv(t)
	e.client = func() *http.Client { return http.DefaultClient }
	if err := config.New(e.dir).SaveHost(other.URL, config.HostConfig{User: "u"}); err != nil {
		t.Fatal(err)
	}
	e.vars["GS_HOST"] = mine.URL
	e.vars["GS_TOKEN"] = secret
	e.run("auth", "status")
	if leaked {
		t.Fatal("GS_TOKEN mandato a un'istanza che non è GS_HOST")
	}
}

func TestLogout(t *testing.T) {
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	e.client = srv.Client
	e.stdin = secret
	if r := e.run("auth", "login", "--hostname", srv.URL, "--with-token"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := e.run("auth", "logout"); r.code != 0 || !strings.Contains(r.errOut, srv.URL) {
		t.Fatalf("logout: %+v", r)
	}
	if _, err := keyring.Get(config.KeyringService, srv.URL); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("token ancora nel portachiavi: %v", err)
	}
	if _, err := os.Stat(e.hostFile(srv.URL)); err == nil {
		t.Error("host ancora configurato")
	}
	if r := e.run("auth", "logout"); r.code != 4 {
		t.Errorf("secondo logout: exit %d", r.code)
	}
}

func TestSetupGitEHelper(t *testing.T) {
	srv := server(t, map[string]string{secret: "alice"})
	e := newEnv(t)
	e.client = srv.Client
	e.stdin = secret
	if r := e.run("auth", "login", "--hostname", srv.URL, "--with-token"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := e.run("auth", "setup-git"); r.code != 0 {
		t.Fatalf("setup-git: %+v", r)
	}
	if len(e.gitArgs) != 2 {
		t.Fatalf("chiamate a git: %v", e.gitArgs)
	}
	key := "credential." + srv.URL + ".helper"
	if e.gitArgs[0][len(e.gitArgs[0])-2] != key || e.gitArgs[0][len(e.gitArgs[0])-1] != "" {
		t.Errorf("azzeramento: %v", e.gitArgs[0])
	}
	if got := e.gitArgs[1][len(e.gitArgs[1])-1]; got != "!'/usr/local/bin/gs' auth git-credential" {
		t.Errorf("helper: %q", got)
	}

	// Il helper risponde per l'host configurato e solo per quello.
	e.stdin = "protocol=http\nhost=" + hostOf(srv) + "\n\n"
	r := e.run("auth", "git-credential", "get")
	if r.code != 0 || r.out != "username=alice\npassword="+secret+"\n" {
		t.Errorf("get: %+v", r)
	}
	for _, in := range []string{"protocol=https\nhost=" + hostOf(srv) + "\n\n", "protocol=http\nhost=altro.test\n\n"} {
		e.stdin = in
		if r := e.run("auth", "git-credential", "get"); r.code != 0 || r.out != "" {
			t.Errorf("host estraneo %q: %+v", in, r)
		}
	}
	e.stdin = "protocol=http\nhost=" + hostOf(srv) + "\n\n"
	if r := e.run("auth", "git-credential", "store"); r.code != 0 || r.out != "" {
		t.Errorf("store: %+v", r)
	}
}

func TestSetupGitSenzaIstanze(t *testing.T) {
	e := newEnv(t)
	if r := e.run("auth", "setup-git"); r.code != 4 {
		t.Errorf("exit %d", r.code)
	}
}
