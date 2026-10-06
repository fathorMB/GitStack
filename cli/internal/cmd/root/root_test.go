package root

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type result struct {
	code        int
	out, errOut string
}

// gs esegue il comando radice con una Factory di test: config in una cartella
// temporanea, nessun remote git, env controllato.
func gs(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("1.2.3", io_)
	if env == nil {
		env = map[string]string{}
	}
	if env[config.EnvConfigDir] == "" {
		env[config.EnvConfigDir] = t.TempDir()
	}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	code := Run(context.Background(), f, args)
	return result{code, out.String(), errOut.String()}
}

func session(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"authMethod":"token","mustChangePassword":false,"scopes":["repo:read"],"user":{"username":"alice","displayName":"Alice","kind":"user"}}`)
}

func TestVersione(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		r := gs(t, nil, args...)
		if r.code != 0 || r.out != "gs version 1.2.3\n" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestHelpElenca12Gruppi(t *testing.T) {
	r := gs(t, nil, "--help")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, g := range []string{"auth", "repo", "issue", "label", "milestone", "search", "browse", "blame", "notification", "api", "skills", "version"} {
		if !strings.Contains(r.out, "\n  "+g+" ") {
			t.Errorf("gruppo %q assente da --help:\n%s", g, r.out)
		}
	}
	if !strings.Contains(r.out, "--hostname") || !strings.Contains(r.out, "--repo") {
		t.Error("flag globali assenti")
	}
}

func TestUsoErrato(t *testing.T) {
	for _, args := range [][]string{{"nonesiste"}, {"--flag-strano"}, {"version", "extra"}, {"api", "--json=nope"}} {
		r := gs(t, nil, args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2 (%q)", args, r.code, r.errOut)
		}
	}
	// Le foglie non ancora implementate escono 2.
	for _, g := range []string{"browse", "blame"} {
		if r := gs(t, nil, g); r.code != 2 || !strings.Contains(r.errOut, "non ancora implementato") {
			t.Errorf("%s: %+v", g, r)
		}
	}
}

func TestApiUserConGSHostETokenDaEnv(t *testing.T) {
	var ua, auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, auth, path = r.Header.Get("User-Agent"), r.Header.Get("Authorization"), r.URL.Path
		session(w, r)
	}))
	defer srv.Close()
	env := map[string]string{"GS_HOST": srv.URL, "GS_TOKEN": "gst_env"}
	f := func(args ...string) result {
		io_, _, out, errOut := cmdutil.Test()
		fac := cmdutil.New("1.2.3", io_)
		fac.Getenv = func(k string) string {
			if k == config.EnvConfigDir {
				return t.TempDir()
			}
			return env[k]
		}
		fac.Git = noGit{}
		fac.HTTPClient = srv.Client
		return result{Run(context.Background(), fac, args), out.String(), errOut.String()}
	}

	r := f("api", "/auth/session")
	if r.code != 0 || !strings.Contains(r.out, "alice") {
		t.Fatalf("%+v", r)
	}
	if path != "/api/v1/auth/session" || ua != "gs/1.2.3" || auth != "Bearer gst_env" {
		t.Errorf("richiesta: %q %q %q", path, ua, auth)
	}

	if r = f("api", "/auth/session", "--jq", ".user.username"); r.out != "alice\n" || r.code != 0 {
		t.Fatalf("--jq: %+v", r)
	}
}

func TestHostnameFlagEIstanzaPredefinita(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/meta") {
			hits++
		}
		if r.Header.Get("Authorization") != "Bearer gst_file" {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated","message":"no"}}`)
			return
		}
		session(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := config.New(dir)
	if err := c.SaveHost(srv.URL, config.HostConfig{User: "alice", Token: "gst_file"}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) result {
		io_, _, out, errOut := cmdutil.Test()
		fac := cmdutil.New("dev", io_)
		fac.Getenv = func(k string) string {
			if k == config.EnvConfigDir {
				return dir
			}
			return ""
		}
		fac.Git = noGit{}
		fac.HTTPClient = srv.Client
		return result{Run(context.Background(), fac, args), out.String(), errOut.String()}
	}
	// Istanza predefinita (nessun GS_HOST, nessun repo) e --hostname esplicito.
	if r := run("api", "/auth/session"); r.code != 0 {
		t.Fatalf("predefinita: %+v", r)
	}
	if r := run("api", "/auth/session", "--hostname", srv.URL); r.code != 0 {
		t.Fatalf("--hostname: %+v", r)
	}
	// --hostname verso un'istanza senza token: exit 4.
	r := run("api", "/auth/session", "--hostname", "altra.test")
	if r.code != 4 {
		t.Errorf("senza token: exit %d (%q)", r.code, r.errOut)
	}
	if hits != 2 {
		t.Errorf("richieste al server: %d", hits)
	}
}

func TestCodiciDiUscitaEErroreJSON(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		errKey string
	}{
		{401, `{"error":{"code":"unauthenticated","message":"token non valido"}}`, 4, "unauthenticated"},
		{403, `{"error":{"code":"insufficient_scope","message":"serve repo:write"}}`, 5, "insufficient_scope"},
		{403, `{"error":{"code":"forbidden","message":"vietato"}}`, 5, "forbidden"},
		{404, `{"error":{"code":"not_found","message":"non trovato"}}`, 6, "not_found"},
		{409, `{"error":{"code":"conflict","message":"conflitto"}}`, 1, "conflict"},
		{500, `boom`, 1, "error"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		run := func(args ...string) result {
			io_, _, out, errOut := cmdutil.Test()
			fac := cmdutil.New("dev", io_)
			fac.Getenv = func(k string) string {
				return map[string]string{"GS_HOST": srv.URL, "GS_TOKEN": "gst_x", config.EnvConfigDir: t.TempDir()}[k]
			}
			fac.Git = noGit{}
			fac.HTTPClient = srv.Client
			return result{Run(context.Background(), fac, args), out.String(), errOut.String()}
		}
		// Testo normale su stderr; il corpo dell'errore va su stdout (gs api).
		r := run("api", "/auth/session")
		if r.code != c.code || !strings.HasPrefix(r.errOut, "gs: ") || strings.TrimSpace(r.out) != c.body {
			t.Errorf("%d: %+v", c.status, r)
		}
		// Modalità JSON: errore JSON su stderr, stdout vuoto.
		for _, args := range [][]string{{"api", "/auth/session", "--jq", ".user"}, {"api", "/auth/session", "--jq", "."}} {
			r = run(args...)
			var e struct {
				Error struct{ Code, Message string }
			}
			if r.code != c.code || strings.TrimSpace(r.out) != c.body || json.Unmarshal([]byte(r.errOut), &e) != nil || e.Error.Message == "" {
				t.Errorf("%d %v: %+v", c.status, args, r)
				continue
			}
			if c.errKey != "error" && e.Error.Code != c.errKey {
				t.Errorf("%d: code %q, atteso %q", c.status, e.Error.Code, c.errKey)
			}
		}
		srv.Close()
	}
}

func TestSenzaIstanzaNeToken(t *testing.T) {
	r := gs(t, nil, "api", "/auth/session")
	if r.code != 4 || !strings.Contains(r.errOut, "nessuna istanza") {
		t.Errorf("%+v", r)
	}
	r = gs(t, map[string]string{"GS_HOST": "h.test"}, "api", "/auth/session", "--jq", ".user")
	var e struct{ Error struct{ Code string } }
	if r.code != 4 || json.Unmarshal([]byte(r.errOut), &e) != nil || e.Error.Code != "not_authenticated" {
		t.Errorf("%+v", r)
	}
}

// Il controllo di versione (G6) è collegato alla radice: major diversa = exit 1,
// minor diversa = avviso su stderr; version e auth login non lo eseguono.
func TestCompatCollegata(t *testing.T) {
	serverVersion := "v2.0.0"
	var metaHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/meta") {
			metaHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"server_version":"`+serverVersion+`","api_version":"v1"}`)
			return
		}
		session(w, r)
	}))
	defer srv.Close()
	run := func(version string, args ...string) result {
		io_, _, out, errOut := cmdutil.Test()
		fac := cmdutil.New(version, io_)
		dir := t.TempDir()
		fac.Getenv = func(k string) string {
			switch k {
			case config.EnvConfigDir:
				return dir
			case "GS_HOST":
				return srv.URL
			case "GS_TOKEN":
				return "gst_x"
			}
			return ""
		}
		fac.Git = noGit{}
		fac.HTTPClient = srv.Client
		return result{Run(context.Background(), fac, args), out.String(), errOut.String()}
	}
	if r := run("v1.0.0", "api", "/auth/session"); r.code != 1 || !strings.Contains(r.errOut, "incompatibile") {
		t.Errorf("major diversa: %+v", r)
	}
	serverVersion = "v1.2.0"
	if r := run("v1.0.0", "api", "/auth/session"); r.code != 0 || !strings.Contains(r.errOut, "avviso") {
		t.Errorf("minor diversa: %+v", r)
	}
	before := metaHits
	if r := run("v1.0.0", "version"); r.code != 0 {
		t.Errorf("version: %+v", r)
	}
	if metaHits != before {
		t.Error("version non deve controllare il server")
	}
}
