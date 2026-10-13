//go:build integration

package stackitest

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

const (
	mirrorSecretKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	mirrorDestUser  = "mirror-bot"
	mirrorDestToken = "ghp_TOKENDIPROVA_giro_completo_42"
	// «example.com» è nel certificato del server di prova (httptest); il
	// resolver del test lo porta all'indirizzo della macchina.
	mirrorDestHost = "example.com"
)

// hostIPv4 è un indirizzo IPv4 della macchina che la policy di uscita (C8)
// ammette: il loopback è sempre bloccato, quindi la destinazione di prova
// ascolta su una scheda vera. Senza, il test è saltato.
func hostIPv4(t *testing.T, eg *mirrors.Egress) netip.Addr {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("indirizzi della macchina non leggibili: %v", err)
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipn.IP.To4())
		if !ok {
			continue
		}
		if eg.CheckIP(mirrorDestHost, ip) == nil {
			return ip
		}
	}
	t.Skip("nessun indirizzo IPv4 non di loopback ammesso dalla policy di uscita")
	return netip.Addr{}
}

// mirrorDest è un secondo server git: un repo bare servito da git
// http-backend dietro TLS con autenticazione Basic, come lo vedrebbe un
// GitHub. Ascolta su tutte le schede, non solo sul loopback.
type mirrorDest struct {
	bare string
	port string
	ca   string
}

func newMirrorDest(t *testing.T) *mirrorDest {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "dest", "dest.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o750); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare)
	cmd.Env = append(cleanEnv(), "HOME="+home, "USERPROFILE="+home, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	// git-http-backend dalla exec-path: il `git` del PATH può essere un wrapper.
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	backendPath := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if runtime.GOOS == "windows" {
		backendPath += ".exe"
	}
	backend := &cgi.Handler{
		Path: backendPath, InheritEnv: []string{"PATH", "SystemRoot", "TEMP", "TMP", "HOME", "USERPROFILE"},
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(bare), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=" + mirrorDestUser,
			"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"},
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(mirrorDestUser+":"+mirrorDestToken))
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	srv.Listener = l
	srv.StartTLS()
	t.Cleanup(srv.Close)

	ca := filepath.Join(base, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return &mirrorDest{bare: bare, port: port, ca: ca}
}

func (d *mirrorDest) url() string { return "https://" + mirrorDestHost + ":" + d.port + "/dest.git" }

// ref è lo sha del ref sulla destinazione ("" se non c'è).
func (e *gitEnv) destRef(d *mirrorDest, ref string) string {
	e.t.Helper()
	out, _ := e.git(e.home, "", "--git-dir="+d.bare, "rev-parse", "--verify", "-q", ref)
	return strings.TrimSpace(out)
}

// TestMirrorGiroCompleto prova sullo stack vero (gateway, identity, git come
// binari, core in-process) il mirror in push verso un secondo server git
// (GIT-181, dopo GIT-179 e GIT-180): si crea il mirror via API, si spingono il
// main e un tag e gli SHA arrivano uguali; se la destinazione diverge il
// mirror si ferma SENZA forzare; risolta la divergenza, «Sincronizza ora»
// lo riporta in pari.
func TestMirrorGiroCompleto(t *testing.T) {
	requireTools(t)
	keys, err := webhooks.NewKeyring("k1", mirrorSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	eg, err := mirrors.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ip := hostIPv4(t, eg)
	eg.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == mirrorDestHost {
			return []netip.Addr{ip}, nil
		}
		return nil, net.UnknownNetworkError("host sconosciuto: " + host)
	}
	dest := newMirrorDest(t)
	// Il servizio git (binario vero) si fida della CA del server di prova per
	// i soli push dei mirror (GITSTACK_GIT_MIRROR_CA_FILE); start() eredita
	// l'ambiente del test. Su Windows git usa schannel e non legge questo file:
	// il test gira in CI (Linux).
	t.Setenv("GITSTACK_GIT_MIRROR_CA_FILE", dest.ca)

	e := newGitEnv(t, httpserver.WithMirrors(httpserver.MirrorConfig{Keys: keys, Egress: eg}))
	eng := &mirrors.Engine{
		Pool: e.pool, Keys: keys, Egress: eg,
		Git: gitclient.New(mustURL(t, e.gitHTTP), coreSecret, 30*time.Second),
	}
	ctx := context.Background()

	// processa: gli eventi git.push diventano mirror in coda (come il consumer
	// durevole di core), poi si eseguono i mirror dovuti. Gli eventi sono tutti
	// quelli del repo: HandlePush è idempotente.
	events := 0
	process := func(wantRuns int) {
		t.Helper()
		if events > 0 {
			for _, p := range e.pushEvents("alice/giro", events) {
				if err := eng.HandlePush(ctx, pkgevents.Envelope{}, p); err != nil {
					t.Fatalf("HandlePush: %v", err)
				}
			}
		}
		n, err := eng.ProcessDue(ctx)
		if err != nil {
			t.Fatalf("ProcessDue: %v", err)
		}
		if n != wantRuns {
			t.Fatalf("mirror eseguiti: %d, attesi %d", n, wantRuns)
		}
	}

	e.createRepo("alice", "giro", "private")
	work := e.initWork(e.httpsURL("alice", e.tokens["alice"], "/alice/giro.git"), map[string]string{"uno.txt": "1\n"})
	e.commitFile(work, "due.txt", "2\n", "secondo")
	e.mustGit(work, "", "push", "-q", "origin", "main")
	e.mustGit(work, "", "tag", "v1")
	e.mustGit(work, "", "push", "-q", "origin", "v1")
	events += 2
	head := func() string { return strings.TrimSpace(e.mustGit(work, "", "rev-parse", "HEAD")) }
	tagSHA := func(tag string) string { return strings.TrimSpace(e.mustGit(work, "", "rev-parse", "refs/tags/"+tag)) }

	// --- 1. crea il mirror via API: il primo push parte da solo -------------
	created := e.gw("POST", "/repos/alice/giro/mirrors",
		map[string]any{"url": dest.url(), "username": mirrorDestUser, "token": mirrorDestToken}, e.bearer("alice"), nil)
	want(t, created, 201, "")
	if strings.Contains(string(created.body), mirrorDestToken) {
		t.Fatalf("il token torna nella risposta: %s", created.body)
	}
	id, _ := created.json()["id"].(string)
	mirrorPath := "/repos/alice/giro/mirrors/" + id
	state := func() string {
		t.Helper()
		r := e.gw("GET", mirrorPath, nil, e.bearer("alice"), nil)
		want(t, r, 200, "")
		s, _ := r.json()["state"].(string)
		return s
	}
	process(1)
	if s := state(); s != "in_sync" {
		t.Fatalf("stato dopo il primo push: %q; mirror: %s", s, e.gw("GET", mirrorPath, nil, e.bearer("alice"), nil).body)
	}
	if got, w := e.destRef(dest, "refs/heads/main"), head(); got != w {
		t.Fatalf("main sulla destinazione %q, sulla sorgente %q", got, w)
	}
	if got, w := e.destRef(dest, "refs/tags/v1"), tagSHA("v1"); got == "" || got != w {
		t.Fatalf("tag v1 sulla destinazione %q, sulla sorgente %q", got, w)
	}

	// --- 2. nuovo push sul main e un tag annotato: la coda riparte ----------
	e.commitFile(work, "tre.txt", "3\n", "terzo")
	e.mustGit(work, "", "tag", "-a", "-m", "versione due", "v2")
	e.mustGit(work, "", "push", "-q", "origin", "main", "v2")
	events++
	process(1)
	if got, w := e.destRef(dest, "refs/heads/main"), head(); got != w {
		t.Fatalf("dopo il secondo push, main sulla destinazione %q, sulla sorgente %q", got, w)
	}
	if got, w := e.destRef(dest, "refs/tags/v2"), tagSHA("v2"); got == "" || got != w {
		t.Fatalf("tag v2 (annotato) sulla destinazione %q, sulla sorgente %q", got, w)
	}
	inSync := head()

	// --- 3. divergenza: la destinazione ha un commit suo sul main -----------
	other := e.clone(dest.bare, "")
	e.commitFile(other, "estraneo.txt", "e\n", "commit fatto direttamente sulla destinazione")
	e.mustGit(other, "", "push", "-q", "origin", "main")
	divergent := strings.TrimSpace(e.mustGit(other, "", "rev-parse", "HEAD"))

	e.commitFile(work, "quattro.txt", "4\n", "quarto")
	e.mustGit(work, "", "push", "-q", "origin", "main")
	events++
	process(1)
	if s := state(); s != "diverged" {
		t.Fatalf("stato con la destinazione divergente: %q", s)
	}
	if got := e.destRef(dest, "refs/heads/main"); got != divergent {
		t.Fatalf("il mirror ha toccato il main della destinazione: %q, atteso %q (nessun force)", got, divergent)
	}
	runs := e.gw("GET", mirrorPath+"/runs", nil, e.bearer("alice"), nil)
	want(t, runs, 200, "")
	if !strings.Contains(string(runs.body), `"diverged"`) {
		t.Fatalf("nessuna esecuzione con esito diverged: %s", runs.body)
	}
	if strings.Contains(string(runs.body), mirrorDestToken) {
		t.Fatalf("il token compare nel log delle esecuzioni: %s", runs.body)
	}
	// Fermo davvero: un altro push non lo rimette in coda, finché non lo si chiede.
	e.commitFile(work, "cinque.txt", "5\n", "quinto")
	e.mustGit(work, "", "push", "-q", "origin", "main")
	events++
	process(0)
	if s := state(); s != "diverged" {
		t.Fatalf("stato dopo un altro push: %q, doveva restare diverged", s)
	}
	if got := e.destRef(dest, "refs/heads/main"); got != divergent {
		t.Fatalf("il main della destinazione è cambiato: %q", got)
	}

	// --- 4. risolta la divergenza (il commit estraneo viene tolto dalla
	// destinazione), «Sincronizza ora» riporta il mirror in pari -------------
	e.mustGit(e.home, "", "--git-dir="+dest.bare, "update-ref", "refs/heads/main", inSync)
	sync := e.gw("POST", mirrorPath+"/sync", nil, e.bearer("alice"), nil)
	want(t, sync, 202, "")
	process(1)
	if s := state(); s != "in_sync" {
		t.Fatalf("stato dopo Sincronizza ora: %q", s)
	}
	if got, w := e.destRef(dest, "refs/heads/main"), head(); got != w {
		t.Fatalf("dopo la ripresa, main sulla destinazione %q, sulla sorgente %q", got, w)
	}
}
