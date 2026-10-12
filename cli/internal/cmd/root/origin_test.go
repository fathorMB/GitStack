package root

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
)

// originGit finge un remote origin con l'URL dato.
type originGit struct{ url string }

func (g originGit) RemoteURL(context.Context, string) (string, error) { return g.url, nil }

// countingServer conta ogni richiesta (anche /meta) e registra se ha visto un
// header Authorization.
type countingServer struct {
	*httptest.Server
	hits int32
	auth int32
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cs.hits, 1)
		if r.Header.Get("Authorization") != "" {
			atomic.AddInt32(&cs.auth, 1)
		}
		if strings.HasSuffix(r.URL.Path, "/meta") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"1.2.3"}`)
			return
		}
		session(w, r)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (c *countingServer) count() int { return int(atomic.LoadInt32(&c.hits)) }

// gsOrigin esegue gs con un remote origin e un env controllati.
func gsOrigin(t *testing.T, origin string, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	f := cmdutil.New("1.2.3", io_)
	e := map[string]string{config.EnvConfigDir: t.TempDir(), cmdutil.EnvNoKeyring: "1"}
	for k, v := range env {
		e[k] = v
	}
	f.Getenv = func(k string) string { return e[k] }
	f.Git = originGit{url: origin}
	return result{Run(context.Background(), f, args), out.String(), errOut.String()}
}

func remoteOn(s *countingServer) string {
	// localhost, non 127.0.0.1: l'host del remote deve differire da quello di GS_HOST.
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	return "http://localhost:" + port + "/acme/web.git"
}

// configForDefault scrive un config.yaml con default_host in una cartella
// temporanea e ne restituisce il percorso.
func configForDefault(t *testing.T, host string) string {
	dir := t.TempDir()
	data := []byte(fmt.Sprintf("default_host: %s\n", host))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// gsOriginWithConfig esegue gs come gsOrigin, ma usa la cartella di config
// fornita invece di una temporanea vuota.
func gsOriginWithConfig(t *testing.T, configDir string, origin string, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	f := cmdutil.New("1.2.3", io_)
	e := map[string]string{config.EnvConfigDir: configDir, cmdutil.EnvNoKeyring: "1"}
	for k, v := range env {
		e[k] = v
	}
	f.Getenv = func(k string) string { return e[k] }
	f.Git = originGit{url: origin}
	return result{Run(context.Background(), f, args), out.String(), errOut.String()}
}

// GIT-189: con GS_HOST impostato e origin su un altro host (github.com nel
// caso reale), gs usa GS_HOST: l'host del remote non riceve né richieste né token.
func TestGSHostVinceSulRemoteOrigin(t *testing.T) {
	inst, other := newCountingServer(t), newCountingServer(t)
	r := gsOrigin(t, remoteOn(other), map[string]string{"GS_HOST": inst.URL, "GS_TOKEN": "gst_env"}, "", "api", "/auth/session")
	if r.code != 0 || !strings.Contains(r.out, "alice") {
		t.Fatalf("%+v", r)
	}
	if other.count() != 0 {
		t.Errorf("l'host del remote ha ricevuto %d richieste", other.count())
	}
	if inst.auth == 0 {
		t.Error("nessuna richiesta autenticata verso GS_HOST")
	}
}

// Senza GS_HOST e senza istanza configurata, un origin qualunque non è
// un'istanza: nessuna richiesta, exit 4.
func TestRemoteNonConfiguratoNonRiceveRichieste(t *testing.T) {
	other := newCountingServer(t)
	for _, args := range [][]string{{"api", "/auth/session"}, {"issue", "list"}, {"repo", "view"}} {
		r := gsOrigin(t, remoteOn(other), map[string]string{"GS_TOKEN": "gst_env"}, "", args...)
		if r.code != 4 {
			t.Errorf("%v: exit %d, atteso 4 (%q)", args, r.code, r.errOut)
		}
	}
	if other.count() != 0 {
		t.Errorf("l'host del remote ha ricevuto %d richieste", other.count())
	}
}

// GS_TOKEN non esce verso un host diverso da GS_HOST, neanche con --hostname:
// exit 4 «nessun token per <host>» e nessuna richiesta all'host richiesto.
func TestGSTokenSoloVersoGSHost(t *testing.T) {
	inst, other := newCountingServer(t), newCountingServer(t)
	env := map[string]string{"GS_HOST": inst.URL, "GS_TOKEN": "gst_env"}
	r := gsOrigin(t, remoteOn(other), env, "", "--hostname", other.URL, "api", "/auth/session")
	if r.code != 4 || !strings.Contains(r.errOut, "nessun token per "+other.URL) {
		t.Fatalf("%+v", r)
	}
	if other.count() != 0 || inst.count() != 0 {
		t.Errorf("richieste: remote %d, GS_HOST %d (attese 0)", other.count(), inst.count())
	}
}

// Con l'istanza scelta e un origin su un altro host, owner/repo non si ricava
// dal remote: errore che nomina i due host, nessuna richiesta all'host del remote.
func TestRepoDalRemoteSoloSeStessoHost(t *testing.T) {
	inst, other := newCountingServer(t), newCountingServer(t)
	env := map[string]string{"GS_HOST": inst.URL, "GS_TOKEN": "gst_env"}
	r := gsOrigin(t, remoteOn(other), env, "", "issue", "list")
	if r.code != 2 || !strings.Contains(r.errOut, "localhost:") || !strings.Contains(r.errOut, inst.URL) {
		t.Fatalf("%+v", r)
	}
	if other.count() != 0 {
		t.Errorf("l'host del remote ha ricevuto %d richieste", other.count())
	}
}

// TestRepoDalRemoteSoloSeStessoHostConPredefinita: con la sola predefinita
// (senza GS_HOST né --hostname) e un origin su un altro host, gs issue list
// risponde exit 2 con un errore che nomina i due host e non fa nessuna
// richiesta. Il caso positivo: un origin sulla predefinita (anche SSH su
// porta 2222) fa la richiesta.
func TestRepoDalRemoteSoloSeStessoHostConPredefinita(t *testing.T) {
	inst, other := newCountingServer(t), newCountingServer(t)
	cfgDir := configForDefault(t, inst.URL)

	// Negativo: origin su altro host, exit 2, zero richieste all'altro host.
	r := gsOriginWithConfig(t, cfgDir, remoteOn(other), map[string]string{"GS_TOKEN": "gst_env"}, "", "issue", "list")
	if r.code != 2 || !strings.Contains(r.errOut, "localhost:") || !strings.Contains(r.errOut, inst.URL) {
		t.Fatalf("negativo: %+v", r)
	}
	if other.count() != 0 {
		t.Errorf("alt host: %d richieste, atteso 0", other.count())
	}

	// Positivo: origin sulla predefinita (stesso URL, non localhost) → request va a inst.
	r = gsOriginWithConfig(t, cfgDir, inst.URL+"/acme/web.git", map[string]string{"GS_TOKEN": "gst_env"}, "", "issue", "list")
	if r.code != 0 {
		t.Fatalf("positivo: %+v", r)
	}
	if inst.count() == 0 {
		t.Error("nessuna richiesta verso l'istanza predefinita")
	}
}

// Il credential helper dà GS_TOKEN solo a GS_HOST, mai all'host del remote.
func TestCredentialHelperNonDaGSTokenAdAltriHost(t *testing.T) {
	env := map[string]string{"GS_HOST": "homehub.local", "GS_TOKEN": "gst_env"}
	r := gsOrigin(t, "https://github.com/acme/web.git", env, "protocol=https\nhost=github.com\n\n", "auth", "git-credential", "get")
	if r.code != 0 || strings.Contains(r.out, "gst_env") || r.out != "" {
		t.Errorf("github.com: %+v", r)
	}
	r = gsOrigin(t, "https://github.com/acme/web.git", env, "protocol=https\nhost=homehub.local\n\n", "auth", "git-credential", "get")
	if r.code != 0 || !strings.Contains(r.out, "password=gst_env") {
		t.Errorf("homehub.local: %+v", r)
	}
}
