package sshd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

const repoID = "0f0f0f0f-1111-4222-8333-444444444444"

// fakeDir fa da identity e da core con i dati in memoria: chiave -> utente,
// utente -> ruolo; l'autorizzazione vera è access.Authorizer, la stessa dello
// smart HTTP.
type fakeDir struct {
	keys     map[string]access.KeyOwner
	roles    map[string]string // userID -> read|write
	archived atomic.Bool
	// unprotected: protezione del branch principale spenta (R9).
	unprotected atomic.Bool
}

func (f *fakeDir) LookupKey(_ context.Context, fp string) (access.KeyOwner, error) {
	u, ok := f.keys[fp]
	if !ok {
		return access.KeyOwner{}, access.ErrUnknownKey
	}
	return u, nil
}

func (f *fakeDir) ResolveRepo(_ context.Context, c trust.Identity, owner, name string) (access.RepoRef, error) {
	if owner != "alice" || name != "app" || f.roles[c.UserID] == "" {
		return access.RepoRef{}, access.ErrNotFound
	}
	return access.RepoRef{ID: repoID, Archived: f.archived.Load(), DefaultBranch: "main", ProtectDefaultBranch: !f.unprotected.Load()}, nil
}

func (f *fakeDir) VerifyToken(context.Context, string) (access.Principal, bool, error) {
	return access.Principal{}, false, nil
}

func (f *fakeDir) HasRole(_ context.Context, userID, _ string, role string) (bool, error) {
	have := f.roles[userID]
	return have == "write" || have == role, nil
}

type clientKey struct {
	signer ssh.Signer
	file   string
	fp     string
}

func newClientKey(t *testing.T) clientKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(f, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return clientKey{signer: s, file: f, fp: ssh.FingerprintSHA256(s.PublicKey())}
}

type env struct {
	srv   *Server
	addr  string
	store *repostore.Store
	dir   *fakeDir
	rw    clientKey // alice, write
	ro    clientKey // bob, read
	off   clientKey // carol, disattivata
	other clientKey // sconosciuta
	hk    string
}

func start(t *testing.T) *env {
	t.Helper()
	for _, bin := range []string{"git", "ssh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s non disponibile", bin)
		}
	}
	root := t.TempDir()
	store, err := repostore.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), repoID, repostore.CreateOptions{
		Files:  []repostore.File{{Path: "README.md", Content: []byte("# app\n")}},
		Author: repostore.Author{Name: "T", Email: "t@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	e := &env{store: store, rw: newClientKey(t), ro: newClientKey(t), off: newClientKey(t), other: newClientKey(t)}
	e.dir = &fakeDir{
		keys: map[string]access.KeyOwner{
			e.rw.fp:  {UserID: "u-alice", Username: "alice", Active: true},
			e.ro.fp:  {UserID: "u-bob", Username: "bob", Active: true},
			e.off.fp: {UserID: "u-carol", Username: "carol", Active: false},
		},
		roles: map[string]string{"u-alice": "write", "u-bob": "read", "u-carol": "write"},
	}
	e.hk = filepath.Join(root, "ssh", "ssh_host_ed25519_key")
	e.launch(t)
	return e
}

func (e *env) launch(t *testing.T) {
	t.Helper()
	hk, err := LoadOrCreateHostKey(e.hk)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := receiverules.Install(filepath.Join(t.TempDir(), "hooks"), receiverules.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Addr: "127.0.0.1:0", HostKey: hk, Auth: &access.Authorizer{Identity: e.dir, Core: e.dir, Disk: e.store}, Keys: e.dir, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	e.srv, e.addr = srv, srv.Addr().String()
}

// gitCmd esegue git con il client ssh reale e la chiave data.
func (e *env) gitCmd(t *testing.T, key clientKey, dir string, args ...string) (string, error) {
	t.Helper()
	known := filepath.Join(t.TempDir(), "known_hosts")
	sshCmd := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=%s -o LogLevel=ERROR",
		filepath.ToSlash(key.file), filepath.ToSlash(known))
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanTestEnv(),
		"GIT_SSH_COMMAND="+sshCmd, "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("git %v: timeout", args)
	}
	return string(out), err
}

func (e *env) url(path string) string { return "ssh://git@" + e.addr + path }

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanTestEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCloneEPush(t *testing.T) {
	e := start(t)
	work := t.TempDir()
	if out, err := e.gitCmd(t, e.rw, work, "clone", e.url("/alice/app.git"), "app"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	app := filepath.Join(work, "app")
	if _, err := os.Stat(filepath.Join(app, "README.md")); err != nil {
		t.Fatal("il clone non contiene README.md")
	}
	if err := os.WriteFile(filepath.Join(app, "nuovo.txt"), []byte("ciao\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, app, "add", ".")
	mustGit(t, app, "commit", "-m", "nuovo")
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	bare, _ := e.store.RepoPath(repoID)
	if got := mustGit(t, bare, "log", "-1", "--format=%s"); got != "nuovo" {
		t.Fatalf("ultimo commit nel repo bare = %q", got)
	}
	// La forma senza .git e senza barra iniziale (scp-like) è la stessa cosa.
	if out, err := e.gitCmd(t, e.rw, work, "ls-remote", e.url("/alice/app")); err != nil {
		t.Fatalf("ls-remote: %v\n%s", err, out)
	}
}

func TestRifiuti(t *testing.T) {
	e := start(t)
	work := t.TempDir()
	cases := []struct {
		name string
		key  clientKey
		path string
	}{
		{"chiave sconosciuta", e.other, "/alice/app.git"},
		{"utente disattivato", e.off, "/alice/app.git"},
		{"repo inesistente", e.rw, "/alice/altro.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := e.gitCmd(t, c.key, work, "clone", e.url(c.path), "x")
			if err == nil {
				t.Fatalf("il clone doveva fallire:\n%s", out)
			}
			if _, statErr := os.Stat(filepath.Join(work, "x", ".git")); statErr == nil {
				t.Fatal("il clone ha scritto un repo")
			}
		})
	}
}

func TestSoloLettura(t *testing.T) {
	e := start(t)
	work := t.TempDir()
	if out, err := e.gitCmd(t, e.ro, work, "clone", e.url("/alice/app.git"), "app"); err != nil {
		t.Fatalf("clone con read: %v\n%s", err, out)
	}
	app := filepath.Join(work, "app")
	if err := os.WriteFile(filepath.Join(app, "x.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, app, "add", ".")
	mustGit(t, app, "commit", "-m", "x")
	out, err := e.gitCmd(t, e.ro, app, "push", "origin", "HEAD")
	if err == nil {
		t.Fatalf("il push con solo read doveva fallire:\n%s", out)
	}
	if !strings.Contains(out, "accesso negato") {
		t.Fatalf("messaggio inatteso:\n%s", out)
	}
	bare, _ := e.store.RepoPath(repoID)
	if got := mustGit(t, bare, "log", "-1", "--format=%s"); got == "x" {
		t.Fatal("il push è passato")
	}
}

func TestRepoArchiviatoRifiutaPush(t *testing.T) {
	e := start(t)
	e.dir.archived.Store(true)
	work := t.TempDir()
	if out, err := e.gitCmd(t, e.rw, work, "clone", e.url("/alice/app.git"), "app"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	app := filepath.Join(work, "app")
	mustGit(t, app, "commit", "--allow-empty", "-m", "vuoto")
	out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD")
	if err == nil || !strings.Contains(out, "archiviato") {
		t.Fatalf("push su repo archiviato: err=%v\n%s", err, out)
	}
	bare, _ := e.store.RepoPath(repoID)
	if n := mustGit(t, bare, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("il repo archiviato è cambiato: %s commit", n)
	}
	// Si legge ancora.
	if out, err := e.gitCmd(t, e.rw, work, "ls-remote", e.url("/alice/app.git")); err != nil {
		t.Fatalf("ls-remote su archiviato: %v\n%s", err, out)
	}
}

func dial(t *testing.T, e *env, user string, key clientKey) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", e.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key.signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test
		Timeout:         10 * time.Second,
	})
}

func TestNessunaShellNeComandiDiversi(t *testing.T) {
	e := start(t)

	t.Run("solo chiave pubblica", func(t *testing.T) {
		_, err := ssh.Dial("tcp", e.addr, &ssh.ClientConfig{
			User:            LoginUser,
			Auth:            []ssh.AuthMethod{ssh.Password("qualunque")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test
			Timeout:         10 * time.Second,
		})
		if err == nil {
			t.Fatal("la password non deve funzionare")
		}
		if !strings.Contains(err.Error(), "publickey") && !strings.Contains(err.Error(), "unable to authenticate") {
			t.Fatalf("errore inatteso: %v", err)
		}
	})
	t.Run("login diverso da git", func(t *testing.T) {
		if _, err := dial(t, e, "root", e.rw); err == nil {
			t.Fatal("solo il login git è ammesso")
		}
	})

	c, err := dial(t, e, LoginUser, e.rw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	t.Run("shell", func(t *testing.T) {
		s, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		if err := s.Shell(); err == nil {
			t.Fatal("la shell doveva essere rifiutata")
		}
	})
	t.Run("pty e subsystem", func(t *testing.T) {
		s, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
			t.Fatal("pty rifiutato atteso")
		}
		if err := s.RequestSubsystem("sftp"); err == nil {
			t.Fatal("sftp rifiutato atteso")
		}
	})
	for _, cmd := range []string{
		"ls", "id", "sh -c id", "cat /etc/passwd",
		"git-upload-pack '/alice/app.git' ; id",
		"git-upload-pack '/alice/app.git' && id",
		"git-upload-pack /alice/app.git --advertise-refs",
		"git-upload-pack '/../etc'",
		"git-upload-pack '/alice/../app.git'",
		"git-upload-pack '/alice/app.git' '/alice/app.git'",
		"git-upload-pack $(id)",
		"git-upload-pack '/alice/`id`.git'",
		"git-lfs-authenticate '/alice/app.git' download",
		"git-upload-archive '/alice/app.git'",
		"git-receive-pack",
		"git --version",
	} {
		t.Run("exec "+cmd, func(t *testing.T) {
			s, err := c.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			out, err := s.CombinedOutput(cmd)
			if err == nil {
				t.Fatalf("%q doveva fallire; output: %s", cmd, out)
			}
			if strings.Contains(string(out), "uid=") || strings.Contains(string(out), "root:") {
				t.Fatalf("%q ha eseguito qualcosa: %s", cmd, out)
			}
		})
	}
	t.Run("canale diverso da session", func(t *testing.T) {
		if _, _, err := c.OpenChannel("direct-tcpip", ssh.Marshal(struct {
			Host string
			Port uint32
			OH   string
			OP   uint32
		}{"127.0.0.1", 80, "127.0.0.1", 1})); err == nil {
			t.Fatal("direct-tcpip rifiutato atteso")
		}
	})
}

func TestChiaveHostStabile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh", "ssh_host_ed25519_key")
	a, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(a.PublicKey()) != ssh.FingerprintSHA256(b.PublicKey()) {
		t.Fatal("la chiave host è cambiata alla seconda lettura")
	}
	if a.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("tipo = %s", a.PublicKey().Type())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// File non valido: errore, mai una chiave nuova che copra quella vecchia.
	bad := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(bad, []byte("non è una chiave"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(bad); err == nil {
		t.Fatal("atteso errore su una chiave illeggibile")
	}
	if got, _ := os.ReadFile(bad); string(got) != "non è una chiave" {
		t.Fatal("il file esistente è stato riscritto")
	}
}

func TestChiaveHostDopoRiavvio(t *testing.T) {
	e := start(t)
	hostFP := func() string {
		var got string
		cfg := &ssh.ClientConfig{
			User: LoginUser,
			Auth: []ssh.AuthMethod{ssh.PublicKeys(e.rw.signer)},
			HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
				got = ssh.FingerprintSHA256(k)
				return nil
			},
			Timeout: 10 * time.Second,
		}
		c, err := ssh.Dial("tcp", e.addr, cfg)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
		return got
	}
	before := hostFP()
	_ = e.srv.Close()
	e.launch(t) // nuovo processo logico: rilegge la chiave dal file
	after := hostFP()
	if before == "" || before != after {
		t.Fatalf("la chiave host è cambiata dopo il riavvio: %q -> %q", before, after)
	}
}

func TestParseCommand(t *testing.T) {
	ok := map[string][3]string{
		"git-upload-pack '/alice/app.git'":    {"upload-pack", "alice", "app"},
		"git-receive-pack '/alice/app.git'":   {"receive-pack", "alice", "app"},
		"git-upload-pack 'alice/app.git'":     {"upload-pack", "alice", "app"},
		"git-upload-pack '/alice/app'":        {"upload-pack", "alice", "app"},
		"git-upload-pack /alice/my.app-1.git": {"upload-pack", "alice", "my.app-1"},
		"git upload-pack '/org-1/x_y.git'":    {"upload-pack", "org-1", "x_y"},
	}
	for in, want := range ok {
		svc, o, n, err := parseCommand(in)
		if err != nil || svc != want[0] || o != want[1] || n != want[2] {
			t.Errorf("%q = %q %q %q %v, atteso %v", in, svc, o, n, err, want)
		}
	}
	for _, in := range []string{
		"", "ls", "git-upload-pack", "git-upload-pack ''", "git-upload-pack '/a'",
		"git-upload-pack '/a/b/c.git'", "git-upload-pack '/a/..'", "git-upload-pack '/a/.x'",
		"git-upload-pack '/a/b' x", "git-upload-pack '/a/b;id'", "git-upload-pack \"/a/b\"",
		"git-upload-archive '/a/b'", "git-receive-pack\t'/a/b'x", "git-upload-pack '/-a/b'",
	} {
		if _, _, _, err := parseCommand(in); err == nil {
			t.Errorf("%q doveva essere rifiutato", in)
		}
	}
}

// cleanTestEnv toglie le GIT_* ereditate (es. GIT_CONFIG_COUNT con hook della
// sessione degli agenti) che cambierebbero il comportamento dei git di prova.
func cleanTestEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}
