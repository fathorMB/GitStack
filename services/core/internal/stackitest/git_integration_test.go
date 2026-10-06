//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Dati del repo "riservato" che nessun caso negativo deve far trapelare.
const (
	secretFile    = "piano-riservato-quarzo.txt"
	secretContent = "CONTENUTO-RISERVATO-b7e41d09"
)

// gitEnv è lo stack completo con un client git reale: gateway, identity e
// git sono binari veri, core è il router vero in-process, Postgres è reale.
type gitEnv struct {
	*stack
	gitHTTP string // http://host:porta del servizio git (smart HTTP)
	sshAddr string // host:porta del server SSH integrato
	natsURL string // nats-server JetStream in-process, a cui il servizio git pubblica git.push
	home    string // HOME isolata per i client git/ssh
	cookies map[string]*http.Cookie
	tokens  map[string]string // token read:resource+write:resource
	roToken map[string]string // token solo read:resource
	keys    map[string]string // file della chiave privata SSH
	// notifier elabora loutbox di core in notifiche (in produzione lo fa il processo core).
	notifier *notify.Engine
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"git", "ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s non è nel PATH", bin)
		}
	}
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nessuno ascolta su %s", addr)
}

// newGitEnv avvia lo stack; opts sono opzioni aggiuntive del router di core
// (per esempio WithAttachments).
func newGitEnv(t *testing.T, opts ...httpserver.Option) *gitEnv {
	t.Helper()
	requireTools(t)
	pool, dsn := dbtest.NewPool(t)
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")
	gitBin := build(t, "../../../git", "git")

	natsSrv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("avvio nats-server: %v", err)
	}
	go natsSrv.Start()
	if !natsSrv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server non pronto entro 10s")
	}
	t.Cleanup(natsSrv.Shutdown)

	identityAddr, gatewayAddr, gitAddr, sshAddr := freeAddr(t), freeAddr(t), freeAddr(t), freeAddr(t)
	_, sshPort, _ := net.SplitHostPort(sshAddr)
	var port int
	_, _ = fmt.Sscanf(sshPort, "%d", &port)

	idc := identityclient.New(mustURL(t, "http://"+identityAddr), serviceSecret, 5*time.Second)
	routerOpts := append([]httpserver.Option{
		httpserver.WithRepoIdentity(idc),
		httpserver.WithGit(gitclient.New(mustURL(t, "http://"+gitAddr), serviceSecret, 30*time.Second)),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "http://" + gitAddr, SSHHost: "127.0.0.1", SSHPort: port}),
	}, opts...)
	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret, routerOpts...))
	t.Cleanup(coreSrv.Close)

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
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	start(t, "git", gitBin, gitAddr,
		"GITSTACK_GIT_ADDR="+gitAddr,
		"GITSTACK_GIT_SSH_ADDR="+sshAddr,
		"GITSTACK_GIT_DATA_DIR="+t.TempDir(),
		"GITSTACK_GIT_NATS_URL="+natsSrv.ClientURL(),
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
	)
	waitTCP(t, sshAddr)

	e := &gitEnv{
		stack:    &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL},
		gitHTTP:  "http://" + gitAddr,
		sshAddr:  sshAddr,
		natsURL:  natsSrv.ClientURL(),
		home:     t.TempDir(),
		cookies:  map[string]*http.Cookie{},
		tokens:   map[string]string{},
		roToken:  map[string]string{},
		keys:     map[string]string{},
		notifier: &notify.Engine{Pool: pool, Identity: idc},
	}
	if err := os.WriteFile(filepath.Join(e.home, ".gitconfig"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.setupUsers()
	return e
}

func (e *gitEnv) session(r reply) *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == "gst_session" {
			return c
		}
	}
	e.t.Fatalf("nessun cookie di sessione: %s", r.body)
	return nil
}

// setupUsers: admin cambia la password, crea alice, bob e carol; ciascuno ha
// un token di lettura+scrittura, uno di sola lettura e una chiave SSH
// (carol no: la sua chiave resta non registrata).
func (e *gitEnv) setupUsers() {
	t := e.t
	const userPassword = "password-di-prova-molto-lunga-1"
	login := e.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, nil, nil)
	want(t, login, 200, "")
	admin := e.session(login)
	want(t, e.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, admin), 204, "")
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	for _, name := range []string{"alice", "bob", "carol"} {
		want(t, e.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, admin), 201, "")
		l := e.gw("POST", "/auth/login", map[string]any{"username": name, "password": userPassword}, nil, nil)
		want(t, l, 200, "")
		ck := e.session(l)
		e.cookies[name] = ck
		rw := e.gw("POST", "/user/tokens", map[string]any{"name": "rw", "scopes": []string{"read:resource", "write:resource"}, "expiresAt": exp}, nil, ck)
		want(t, rw, 201, "")
		e.tokens[name], _ = rw.json()["token"].(string)
		ro := e.gw("POST", "/user/tokens", map[string]any{"name": "ro", "scopes": []string{"read:resource"}, "expiresAt": exp}, nil, ck)
		want(t, ro, 201, "")
		e.roToken[name], _ = ro.json()["token"].(string)

		key := filepath.Join(e.home, "id_"+name)
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name+"@test", "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
		e.keys[name] = filepath.ToSlash(key)
		if name == "carol" {
			continue // chiave non registrata
		}
		pub, err := os.ReadFile(key + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		want(t, e.gw("POST", "/user/ssh-keys", map[string]any{"title": "test", "publicKey": strings.TrimSpace(string(pub))}, nil, ck), 201, "")
	}
}

func (e *gitEnv) bearer(name string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + e.tokens[name]}
}

// createRepo crea un repo via API come `owner` e ne ritorna l'id.
func (e *gitEnv) createRepo(owner, name, visibility string) string {
	e.t.Helper()
	r := e.gw("POST", "/repos", map[string]any{"owner": owner, "name": name, "visibility": visibility}, e.bearer(owner), nil)
	want(e.t, r, 201, "")
	id, _ := r.json()["id"].(string)
	return id
}

func (e *gitEnv) patchRepo(owner, name string, body map[string]any) {
	e.t.Helper()
	want(e.t, e.gw("PATCH", "/repos/"+owner+"/"+name, body, e.bearer(owner), nil), 200, "")
}

func (e *gitEnv) httpsURL(user, token, path string) string {
	return strings.Replace(e.gitHTTP, "http://", "http://"+user+":"+token+"@", 1) + path
}

func (e *gitEnv) sshURL(path string) string { return "ssh://git@" + e.sshAddr + path }

// git esegue il client git reale, con HOME isolata, senza prompt né credential
// helper; `key` (se non vuota) è la chiave SSH da usare.
func (e *gitEnv) git(dir, key string, args ...string) (string, error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	full := append([]string{"-c", "credential.helper=", "-c", "core.autocrlf=false", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	sshCmd := "ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=" + filepath.ToSlash(filepath.Join(e.home, "known_hosts")) + " -o LogLevel=ERROR -o IdentitiesOnly=yes"
	if key != "" {
		sshCmd += " -i " + key
	}
	cmd.Env = append(cleanEnv(),
		"HOME="+e.home, "USERPROFILE="+e.home, "GIT_CONFIG_GLOBAL="+filepath.Join(e.home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=",
		"GIT_SSH_COMMAND="+sshCmd,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// cleanEnv toglie dall'ambiente le variabili GIT_CONFIG_* ereditate (hook e
// regole dell'ambiente di sviluppo): il client di prova deve comportarsi come
// uno qualsiasi, con la sola configurazione che gli diamo.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_CONFIG_") {
			env = append(env, kv)
		}
	}
	return env
}

func (e *gitEnv) mustGit(dir, key string, args ...string) string {
	e.t.Helper()
	out, err := e.git(dir, key, args...)
	if err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// newWork crea un clone di lavoro (cartella nuova) dell'URL dato.
func (e *gitEnv) clone(url, key string) string {
	e.t.Helper()
	dir := filepath.Join(e.t.TempDir(), "w")
	e.mustGit(filepath.Dir(dir), key, "clone", url, dir)
	return dir
}

func (e *gitEnv) commitFile(dir, name, content, msg string) {
	e.t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.mustGit(dir, "", "add", "-A")
	e.mustGit(dir, "", "commit", "-q", "-m", msg)
}

// initWork crea un repo locale con un commit, collegato a `url` come origin.
func (e *gitEnv) initWork(url string, files map[string]string) string {
	e.t.Helper()
	dir := filepath.Join(e.t.TempDir(), "w")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		e.t.Fatal(err)
	}
	e.mustGit(dir, "", "init", "-q", "-b", "main")
	e.mustGit(dir, "", "remote", "add", "origin", url)
	for n, c := range files {
		e.commitFile(dir, n, c, "aggiunge "+n)
	}
	return dir
}

// remoteHead è lo sha di refs/heads/main sul server, letto con ls-remote.
func (e *gitEnv) remoteHead(url, key string) string {
	e.t.Helper()
	out := e.mustGit(e.home, key, "ls-remote", url, "refs/heads/main")
	f := strings.Fields(out)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// leakCheck: né l'output di git né un corpo HTTP devono contenere dati del
// repo (nome di file, contenuto, sha completo o abbreviato).
func leakCheck(t *testing.T, what, out string, shas ...string) {
	t.Helper()
	needles := append([]string{secretFile, secretContent}, shas...)
	for _, s := range shas {
		if len(s) > 7 {
			needles = append(needles, s[:7])
		}
	}
	for _, n := range needles {
		if n != "" && strings.Contains(out, n) {
			t.Errorf("%s: trapela %q:\n%s", what, n, out)
		}
	}
}

func (e *gitEnv) httpGet(path, user, token string) (int, string, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.gitHTTP+path, nil)
	if token != "" {
		req.SetBasicAuth(user, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

const upload = "/info/refs?service=git-upload-pack"

// TestGitClientReale prova il flusso git completo con client git e ssh veri
// contro lo stack: gateway, identity, core, servizio git e Postgres.
func TestGitClientReale(t *testing.T) {
	e := newGitEnv(t)

	// Repo privato di alice con un file riservato.
	repoID := e.createRepo("alice", "riservato", "private")
	aliceURL := e.httpsURL("alice", e.tokens["alice"], "/alice/riservato.git")
	work := e.initWork(aliceURL, map[string]string{secretFile: secretContent + "\n"})
	e.mustGit(work, "", "push", "-q", "-u", "origin", "main")
	secretSHA := strings.TrimSpace(e.mustGit(work, "", "rev-parse", "HEAD"))
	leaks := []string{secretSHA, repoID}

	t.Run("https_clone_push_pull", func(t *testing.T) {
		a := e.clone(aliceURL, "")
		if b, err := os.ReadFile(filepath.Join(a, secretFile)); err != nil || !strings.Contains(string(b), secretContent) {
			t.Fatalf("il clone non ha il file: %v", err)
		}
		e.commitFile(a, "nuovo-https.txt", "https\n", "da https")
		e.mustGit(a, "", "push", "-q", "origin", "main")
		e.mustGit(work, "", "pull", "-q", "--ff-only", "origin", "main")
		if _, err := os.Stat(filepath.Join(work, "nuovo-https.txt")); err != nil {
			t.Fatalf("il pull non ha portato il commit: %v", err)
		}
		// Il repo creato via API espone gli indirizzi di clone dell'installazione.
		r := e.gw("GET", "/repos/alice/riservato", nil, e.bearer("alice"), nil)
		want(t, r, 200, "")
		urls, _ := r.json()["cloneUrls"].(map[string]any)
		if urls["https"] != e.gitHTTP+"/alice/riservato.git" || !strings.HasPrefix(fmt.Sprint(urls["ssh"]), "ssh://git@127.0.0.1:") {
			t.Errorf("cloneUrls = %v", urls)
		}
	})

	t.Run("ssh_clone_push_pull", func(t *testing.T) {
		url := e.sshURL("/alice/riservato.git")
		a := e.clone(url, e.keys["alice"])
		if _, err := os.Stat(filepath.Join(a, secretFile)); err != nil {
			t.Fatalf("il clone SSH non ha il file: %v", err)
		}
		e.commitFile(a, "nuovo-ssh.txt", "ssh\n", "da ssh")
		e.mustGit(a, e.keys["alice"], "push", "-q", "origin", "main")
		// pull via HTTPS dal clone di lavoro: vede il commit spinto via SSH
		e.mustGit(work, "", "pull", "-q", "--ff-only", "origin", "main")
		if _, err := os.Stat(filepath.Join(work, "nuovo-ssh.txt")); err != nil {
			t.Fatalf("il pull HTTPS non ha portato il commit SSH: %v", err)
		}
		// e pull SSH di un commit spinto via HTTPS
		e.commitFile(work, "ancora-https.txt", "x\n", "https di nuovo")
		e.mustGit(work, "", "push", "-q", "origin", "main")
		e.mustGit(a, e.keys["alice"], "pull", "-q", "--ff-only", "origin", "main")
		if _, err := os.Stat(filepath.Join(a, "ancora-https.txt")); err != nil {
			t.Fatalf("il pull SSH non ha portato il commit HTTPS: %v", err)
		}
	})

	t.Run("senza_credenziali", func(t *testing.T) {
		head := e.remoteHead(aliceURL, "")
		// HTTPS: 401 con WWW-Authenticate, corpo senza dati del repo.
		status, body, hdr := e.httpGet("/alice/riservato.git"+upload, "", "")
		if status != 401 || !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Basic") {
			t.Errorf("senza credenziali: %d %v", status, hdr)
		}
		leakCheck(t, "401 senza credenziali", body, leaks...)
		// token inventato e token revocabile ma sbagliato: 401
		status, body, _ = e.httpGet("/alice/riservato.git"+upload, "alice", "gst_inventato")
		if status != 401 {
			t.Errorf("token inventato: %d", status)
		}
		leakCheck(t, "401 token inventato", body, leaks...)
		// il client git non entra
		out, err := e.git(e.home, "", "clone", e.gitHTTP+"/alice/riservato.git", filepath.Join(t.TempDir(), "x"))
		if err == nil {
			t.Fatalf("clone anonimo riuscito:\n%s", out)
		}
		leakCheck(t, "clone anonimo", out, leaks...)
		// la password dell'utente non è un token
		status, _, _ = e.httpGet("/alice/riservato.git"+upload, "alice", "password-di-prova-molto-lunga-1")
		if status != 401 {
			t.Errorf("password al posto del token: %d", status)
		}
		// SSH con una chiave non registrata (carol)
		out, err = e.git(e.home, e.keys["carol"], "clone", e.sshURL("/alice/riservato.git"), filepath.Join(t.TempDir(), "y"))
		if err == nil {
			t.Fatalf("clone SSH con chiave sconosciuta riuscito:\n%s", out)
		}
		leakCheck(t, "clone SSH chiave sconosciuta", out, leaks...)
		if got := e.remoteHead(aliceURL, ""); got != head {
			t.Errorf("head cambiato: %s -> %s", head, got)
		}
	})

	t.Run("utente_senza_permesso", func(t *testing.T) {
		// bob ha token e chiave validi ma nessun grant sul repo privato.
		bobURL := e.httpsURL("bob", e.tokens["bob"], "/alice/riservato.git")
		out, err := e.git(e.home, "", "clone", bobURL, filepath.Join(t.TempDir(), "x"))
		if err == nil {
			t.Fatalf("bob ha clonato un repo privato:\n%s", out)
		}
		leakCheck(t, "clone HTTPS di bob", out, leaks...)
		// Stessa risposta di un repo che non esiste: nessuna rivelazione.
		s1, b1, _ := e.httpGet("/alice/riservato.git"+upload, "bob", e.tokens["bob"])
		s2, b2, _ := e.httpGet("/alice/non-esiste.git"+upload, "bob", e.tokens["bob"])
		if s1 != 404 || s2 != 404 || strings.ReplaceAll(b1, "riservato", "X") != strings.ReplaceAll(b2, "non-esiste", "X") {
			t.Errorf("repo negato %d %q / inesistente %d %q", s1, b1, s2, b2)
		}
		leakCheck(t, "404 di bob", b1, leaks...)
		// push di bob: stesso rifiuto
		w := e.initWork(bobURL, map[string]string{"x.txt": "x\n"})
		if out, err := e.git(w, "", "push", "origin", "main"); err == nil {
			t.Fatalf("bob ha spinto su un repo privato:\n%s", out)
		} else {
			leakCheck(t, "push HTTPS di bob", out, leaks...)
		}
		// SSH
		out, err = e.git(e.home, e.keys["bob"], "clone", e.sshURL("/alice/riservato.git"), filepath.Join(t.TempDir(), "y"))
		if err == nil {
			t.Fatalf("bob ha clonato via SSH:\n%s", out)
		}
		leakCheck(t, "clone SSH di bob", out, leaks...)
		// API: 404, non 403, e senza dati
		r := e.gw("GET", "/repos/alice/riservato", nil, e.bearer("bob"), nil)
		want(t, r, 404, "")
		leakCheck(t, "GET /repos di bob", string(r.body), leaks...)
		// il repo è intatto e alice lo legge ancora
		if e.remoteHead(aliceURL, "") == "" {
			t.Error("repo di alice non leggibile")
		}
	})

	t.Run("token_di_sola_lettura", func(t *testing.T) {
		roURL := e.httpsURL("alice", e.roToken["alice"], "/alice/riservato.git")
		a := e.clone(roURL, "") // legge
		e.commitFile(a, "ro.txt", "ro\n", "ro")
		head := e.remoteHead(aliceURL, "")
		out, err := e.git(a, "", "push", "origin", "main")
		if err == nil || !strings.Contains(out, "403") {
			t.Fatalf("push con token read:resource: err=%v\n%s", err, out)
		}
		leakCheck(t, "push con token di sola lettura", out, leaks...)
		if got := e.remoteHead(aliceURL, ""); got != head {
			t.Errorf("head cambiato: %s -> %s", head, got)
		}
	})

	t.Run("repo_eliminato", func(t *testing.T) {
		id := e.createRepo("alice", "effimero", "private")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/effimero.git")
		w := e.initWork(u, map[string]string{secretFile: secretContent + "\n"})
		e.mustGit(w, "", "push", "-q", "origin", "main")
		sha := strings.TrimSpace(e.mustGit(w, "", "rev-parse", "HEAD"))
		a := e.clone(u, "") // prima di eliminarlo si legge
		want(t, e.gw("DELETE", "/repos/alice/effimero", nil, e.bearer("alice"), nil), 204, "")

		dead := []string{sha, id}
		out, err := e.git(e.home, "", "clone", u, filepath.Join(t.TempDir(), "x"))
		if err == nil {
			t.Fatalf("clone di un repo eliminato riuscito:\n%s", out)
		}
		leakCheck(t, "clone HTTPS repo eliminato", out, dead...)
		if out, err := e.git(a, "", "push", "origin", "main"); err == nil {
			t.Fatalf("push su un repo eliminato riuscito:\n%s", out)
		} else {
			leakCheck(t, "push HTTPS repo eliminato", out, dead...)
		}
		status, body, _ := e.httpGet("/alice/effimero.git"+upload, "alice", e.tokens["alice"])
		if status != 404 {
			t.Errorf("repo eliminato: %d", status)
		}
		leakCheck(t, "404 repo eliminato", body, dead...)
		out, err = e.git(e.home, e.keys["alice"], "clone", e.sshURL("/alice/effimero.git"), filepath.Join(t.TempDir(), "y"))
		if err == nil {
			t.Fatalf("clone SSH di un repo eliminato riuscito:\n%s", out)
		}
		leakCheck(t, "clone SSH repo eliminato", out, dead...)
		r := e.gw("GET", "/repos/alice/effimero", nil, e.bearer("alice"), nil)
		want(t, r, 404, "")
		leakCheck(t, "GET /repos repo eliminato", string(r.body), id)
	})

	t.Run("repo_interno_leggibile_non_scrivibile", func(t *testing.T) {
		e.createRepo("alice", "comune", "internal")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/comune.git")
		w := e.initWork(u, map[string]string{"LEGGIMI.txt": "per tutti\n"})
		e.mustGit(w, "", "push", "-q", "origin", "main")
		head := e.remoteHead(u, "")

		// bob (nessun grant) legge via HTTPS e via SSH...
		bobURL := e.httpsURL("bob", e.tokens["bob"], "/alice/comune.git")
		b := e.clone(bobURL, "")
		if _, err := os.Stat(filepath.Join(b, "LEGGIMI.txt")); err != nil {
			t.Fatal(err)
		}
		bs := e.clone(e.sshURL("/alice/comune.git"), e.keys["bob"])
		// ...ma non scrive: né HTTPS né SSH, e il repo non cambia.
		e.commitFile(b, "bob.txt", "bob\n", "bob")
		out, err := e.git(b, "", "push", "origin", "main")
		if err == nil || !strings.Contains(out, "403") {
			t.Fatalf("bob ha spinto su un repo interno (HTTPS): err=%v\n%s", err, out)
		}
		e.commitFile(bs, "bob-ssh.txt", "bob\n", "bob ssh")
		if out, err := e.git(bs, e.keys["bob"], "push", "origin", "main"); err == nil {
			t.Fatalf("bob ha spinto su un repo interno (SSH):\n%s", out)
		}
		if got := e.remoteHead(u, ""); got != head {
			t.Errorf("head cambiato dopo i push negati: %s -> %s", head, got)
		}
		// il repo interno è leggibile da bob anche via API
		want(t, e.gw("GET", "/repos/alice/comune", nil, e.bearer("bob"), nil), 200, "")
		// carol, senza chiave registrata, non entra via SSH nemmeno su un repo interno
		if out, err := e.git(e.home, e.keys["carol"], "clone", e.sshURL("/alice/comune.git"), filepath.Join(t.TempDir(), "z")); err == nil {
			t.Fatalf("chiave non registrata ha letto:\n%s", out)
		}
	})

	t.Run("R6_file_oltre_100MB", func(t *testing.T) {
		e.createRepo("alice", "regole-r6", "private")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/regole-r6.git")
		w := e.initWork(u, map[string]string{"piccolo.txt": "ciao\n"})
		e.mustGit(w, "", "push", "-q", "origin", "main")
		head := e.remoteHead(u, "")
		const limit = 100 << 20
		big := filepath.Join(w, "dir con spazi", "grande.bin")
		if err := os.MkdirAll(filepath.Dir(big), 0o750); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(big)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(limit + 1); err != nil { // generato al volo: zeri, si comprime
			t.Fatal(err)
		}
		_ = f.Close()
		e.mustGit(w, "", "add", "-A")
		e.mustGit(w, "", "commit", "-q", "-m", "troppo grande")
		for name, run := range map[string]func() (string, error){
			"HTTPS": func() (string, error) { return e.git(w, "", "push", "origin", "main") },
			"SSH": func() (string, error) {
				return e.git(w, e.keys["alice"], "push", e.sshURL("/alice/regole-r6.git"), "main")
			},
		} {
			out, err := run()
			if err == nil {
				t.Fatalf("R6 %s: push oltre soglia riuscito:\n%s", name, out)
			}
			for _, w := range []string{"remote: gitstack: push rifiutato", "grande.bin", "104857601 byte"} {
				if !strings.Contains(out, w) {
					t.Errorf("R6 %s: manca %q:\n%s", name, w, out)
				}
			}
			leakCheck(t, "R6 "+name, out, leaks...)
			if got := e.remoteHead(u, ""); got != head {
				t.Errorf("R6 %s: il repo ha ricevuto il push (%s -> %s)", name, head, got)
			}
		}
		// Tolto il file, il push passa.
		e.mustGit(w, "", "reset", "-q", "--hard", "HEAD~1")
		e.commitFile(w, "ok.txt", "ok\n", "ok")
		e.mustGit(w, "", "push", "-q", "origin", "main")
	})

	t.Run("R9_branch_principale_protetto", func(t *testing.T) {
		e.createRepo("alice", "regole-r9", "private")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/regole-r9.git")
		w := e.initWork(u, map[string]string{"a.txt": "a\n"})
		e.commitFile(w, "b.txt", "b\n", "secondo")
		e.mustGit(w, "", "push", "-q", "origin", "main")
		head := e.remoteHead(u, "")

		// riscrive la storia: force-push e eliminazione del principale rifiutati
		e.mustGit(w, "", "commit", "-q", "--amend", "-m", "riscritto")
		for name, key := range map[string]string{"HTTPS": "", "SSH": e.keys["alice"]} {
			remote := "origin"
			if name == "SSH" {
				remote = e.sshURL("/alice/regole-r9.git")
			}
			out, err := e.git(w, key, "push", "--force", remote, "main")
			if err == nil || !strings.Contains(out, "gitstack: push rifiutato") {
				t.Fatalf("R9 %s: force-push: err=%v\n%s", name, err, out)
			}
			out, err = e.git(w, key, "push", remote, ":main")
			if err == nil || !strings.Contains(out, "gitstack: push rifiutato") {
				t.Fatalf("R9 %s: eliminazione: err=%v\n%s", name, err, out)
			}
			if got := e.remoteHead(u, ""); got != head {
				t.Errorf("R9 %s: main cambiato (%s -> %s)", name, head, got)
			}
		}
		// gli altri branch sono liberi, anche in force-push
		e.mustGit(w, "", "push", "-q", "origin", "main:altro")
		e.mustGit(w, "", "push", "-q", "--force", "origin", "main:altro")
		e.mustGit(w, "", "push", "-q", "origin", ":altro")
		// spenta la protezione, il force-push passa
		e.patchRepo("alice", "regole-r9", map[string]any{"protectDefaultBranch": false})
		e.mustGit(w, "", "push", "-q", "--force", "origin", "main")
		if got := e.remoteHead(u, ""); got == head {
			t.Error("R9: con la protezione spenta il force-push non ha cambiato main")
		}
	})

	t.Run("R10_repo_archiviato", func(t *testing.T) {
		e.createRepo("alice", "regole-r10", "private")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/regole-r10.git")
		w := e.initWork(u, map[string]string{"a.txt": "a\n"})
		e.mustGit(w, "", "push", "-q", "origin", "main")
		head := e.remoteHead(u, "")
		e.patchRepo("alice", "regole-r10", map[string]any{"archived": true})

		// si legge ancora...
		e.clone(u, "")
		e.clone(e.sshURL("/alice/regole-r10.git"), e.keys["alice"])
		// ...ma non si scrive
		e.commitFile(w, "b.txt", "b\n", "dopo archivio")
		out, err := e.git(w, "", "push", "origin", "main")
		if err == nil || !strings.Contains(out, "403") {
			t.Fatalf("R10 HTTPS: err=%v\n%s", err, out)
		}
		if out, err := e.git(w, e.keys["alice"], "push", e.sshURL("/alice/regole-r10.git"), "main"); err == nil {
			t.Fatalf("R10 SSH: push riuscito:\n%s", out)
		}
		if got := e.remoteHead(u, ""); got != head {
			t.Errorf("R10: head cambiato (%s -> %s)", head, got)
		}
		// riattivato, si scrive di nuovo
		e.patchRepo("alice", "regole-r10", map[string]any{"archived": false})
		e.mustGit(w, "", "push", "-q", "origin", "main")
	})

	t.Run("evento_git_push_su_nats", func(t *testing.T) {
		e.createRepo("alice", "evento", "internal")
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/evento.git")
		w := e.initWork(u, map[string]string{"uno.txt": "1\n"})
		e.commitFile(w, "due.txt", "2\n", "secondo")
		e.mustGit(w, "", "push", "-q", "origin", "main")
		first := strings.TrimSpace(e.mustGit(w, "", "rev-parse", "HEAD"))

		evs := e.pushEvents("alice/evento", 1)
		p := evs[0]
		if p.Repo.FullName != "alice/evento" || p.Repo.DefaultBranch != "main" || p.Pusher.Username != "alice" || len(p.Refs) != 1 {
			t.Fatalf("evento HTTPS: %+v", p)
		}
		r := p.Refs[0]
		if r.Ref != "refs/heads/main" || r.After != first || r.Before != strings.Repeat("0", 40) || r.Forced || !r.IsDefaultBranch || len(r.Commits) != 2 {
			t.Errorf("ref HTTPS: %+v", r)
		}

		// un push via SSH: secondo evento, con before = after del primo
		a := e.clone(e.sshURL("/alice/evento.git"), e.keys["alice"])
		e.commitFile(a, "tre.txt", "3\n", "terzo")
		e.mustGit(a, e.keys["alice"], "push", "-q", "origin", "main")
		second := strings.TrimSpace(e.mustGit(a, "", "rev-parse", "HEAD"))
		evs = e.pushEvents("alice/evento", 2)
		r = evs[1].Refs[0]
		if evs[1].Pusher.Username != "alice" || r.Before != first || r.After != second || len(r.Commits) != 1 || !strings.HasPrefix(r.Commits[0].Message, "terzo") {
			t.Errorf("evento SSH: %+v", evs[1])
		}

		// un push negato (bob legge il repo interno ma non scrive) non pubblica niente
		b := e.clone(e.httpsURL("bob", e.tokens["bob"], "/alice/evento.git"), "")
		e.commitFile(b, "bob.txt", "b\n", "bob")
		if out, err := e.git(b, "", "push", "origin", "main"); err == nil {
			t.Fatalf("push di bob riuscito:\n%s", out)
		}
		time.Sleep(2 * time.Second)
		if got := e.pushEvents("alice/evento", 2); len(got) != 2 {
			t.Errorf("eventi dopo un push negato: %d, attesi 2", len(got))
		}
	})
}

// pushEvents legge dallo stream GIT gli eventi git.push di `fullName`, nell'ordine
// di pubblicazione, ripetendo la lettura (l'evento esce dopo la risposta al client)
// finché ne trova almeno `min` o scade il tempo. Ogni evento passa dal registro
// di pkg/events con il Decoder di gitpush, come in un consumer vero.
func (e *gitEnv) pushEvents(fullName string, min int) []gitpush.Payload {
	e.t.Helper()
	nc, err := nats.Connect(e.natsURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		e.t.Fatal(err)
	}
	reg := pkgevents.NewRegistry()
	gitpush.Register(reg)
	var out []gitpush.Payload
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out = out[:0]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cons, err := js.CreateOrUpdateConsumer(ctx, "GIT", jetstream.ConsumerConfig{AckPolicy: jetstream.AckNonePolicy, FilterSubject: gitpush.Name})
		cancel()
		if err == nil {
			batch, ferr := cons.Fetch(100, jetstream.FetchMaxWait(time.Second))
			if ferr == nil {
				for m := range batch.Messages() {
					var env pkgevents.Envelope
					if json.Unmarshal(m.Data(), &env) != nil {
						continue
					}
					v, derr := reg.Decode(env)
					if derr != nil {
						e.t.Fatalf("decodifica dell'evento: %v", derr)
					}
					if p, ok := v.(gitpush.Payload); ok && p.Repo.FullName == fullName {
						out = append(out, p)
					}
				}
			}
		}
		if len(out) >= min {
			return append([]gitpush.Payload(nil), out...)
		}
		time.Sleep(300 * time.Millisecond)
	}
	e.t.Fatalf("eventi git.push di %s: %d, attesi almeno %d", fullName, len(out), min)
	return nil
}
