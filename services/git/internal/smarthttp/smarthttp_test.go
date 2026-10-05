package smarthttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/smarthttp"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
	carol = "33333333-3333-4333-8333-333333333333"

	repoPriv = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" // alice/priv, privato
	repoInt  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" // alice/shared, interno
	repoGone = "cccccccc-cccc-4ccc-8ccc-cccccccccccc" // alice/gone, nel cestino
	repoArch = "dddddddd-dddd-4ddd-8ddd-dddddddddddd" // alice/arch, archiviato
)

// fakeIdentity: token → principal; ruoli per (utente, repo).
type fakeIdentity struct {
	tokens map[string]access.Principal
	roles  map[[2]string]string // {user, repo} → ruolo
	// internal: repo leggibili da ogni utente (P3).
	internal map[string]bool
}

func rank(r string) int { return map[string]int{"read": 1, "write": 2, "admin": 3}[r] }

func (f *fakeIdentity) VerifyToken(_ context.Context, tok string) (access.Principal, bool, error) {
	p, ok := f.tokens[tok]
	return p, ok, nil
}

func (f *fakeIdentity) HasRole(_ context.Context, user, repo, role string) (bool, error) {
	if rank(f.roles[[2]string{user, repo}]) >= rank(role) {
		return true, nil
	}
	return role == "read" && f.internal[repo], nil
}

// fakeCore: come core, 404 se non leggibile.
type fakeCore struct {
	id *fakeIdentity
	by map[string]string // owner/name → repoId
	// archived: repo archiviati (R10).
	archived map[string]bool
	// unprotected: repo con la protezione del branch principale spenta (R9).
	unprotected map[string]bool
}

func (c *fakeCore) ResolveRepo(ctx context.Context, caller trust.Identity, owner, name string) (access.RepoRef, error) {
	rid, ok := c.by[owner+"/"+name]
	if !ok {
		return access.RepoRef{}, access.ErrNotFound
	}
	if ok, _ := c.id.HasRole(ctx, caller.UserID, rid, "read"); !ok {
		return access.RepoRef{}, access.ErrNotFound
	}
	return access.RepoRef{ID: rid, Archived: c.archived[rid], DefaultBranch: "main", ProtectDefaultBranch: !c.unprotected[rid]}, nil
}

func gitBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git non installato")
	}
	return p
}

type env struct {
	srv   *httptest.Server
	store *repostore.Store
	core  *fakeCore
}

func setup(t *testing.T) *env { return setupWith(t, receiverules.DefaultLimits()) }

func setupWith(t *testing.T, limits receiverules.Limits) *env {
	t.Helper()
	bin := gitBin(t)
	store, err := repostore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := repostore.CreateOptions{
		Files:  []repostore.File{{Path: "README.md", Content: []byte("# ciao\n")}},
		Author: repostore.Author{Name: "Alice", Email: "alice@example.com"},
	}
	for _, id := range []string{repoPriv, repoInt, repoGone, repoArch} {
		if _, err := store.Create(ctx, id, opts); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Trash(repoGone); err != nil {
		t.Fatal(err)
	}
	rw := []string{access.ScopeRead, access.ScopeWrite}
	ident := &fakeIdentity{
		tokens: map[string]access.Principal{
			"gst_alice": {UserID: alice, Username: "alice", Scopes: rw},
			"gst_bob":   {UserID: bob, Username: "bob", Scopes: rw},
			"gst_carol": {UserID: carol, Username: "carol", Scopes: rw},
			// alice con un token di sola lettura (senza write:resource).
			"gst_alice_ro": {UserID: alice, Username: "alice", Scopes: []string{access.ScopeRead}},
			// bob ha il ruolo write su priv ma il token è solo read.
			"gst_bob_ro": {UserID: bob, Username: "bob", Scopes: []string{access.ScopeRead}},
		},
		roles: map[[2]string]string{
			{alice, repoPriv}: "admin", {alice, repoInt}: "admin", {alice, repoGone}: "admin", {alice, repoArch}: "admin",
			{bob, repoPriv}: "read",
		},
		internal: map[string]bool{repoInt: true},
	}
	core := &fakeCore{id: ident, by: map[string]string{
		"alice/priv": repoPriv, "alice/shared": repoInt, "alice/gone": repoGone, "alice/arch": repoArch,
	}, archived: map[string]bool{repoArch: true}, unprotected: map[string]bool{}}
	rules, err := receiverules.Install(filepath.Join(t.TempDir(), "hooks"), limits)
	if err != nil {
		t.Fatal(err)
	}
	h := &smarthttp.Handler{
		Auth:   &access.Authorizer{Identity: ident, Core: core, Disk: store},
		GitBin: bin,
		Rules:  rules,
	}
	srv := httptest.NewServer(httpserver.NewRouter(httpserver.Deps{Store: store, Secret: "s", Git: h}))
	t.Cleanup(srv.Close)
	return &env{srv: srv, store: store, core: core}
}

func (e *env) url(user, token, path string) string {
	u, _ := url.Parse(e.srv.URL)
	if token != "" {
		u.User = url.UserPassword(user, token)
	}
	u.Path = path
	return u.String()
}

// git lancia il client git reale, isolato dalla configurazione dell'host.
func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cleanEnv toglie le GIT_* ereditate (config via ambiente, hook dell'host).
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestCloneEPushEPull(t *testing.T) {
	e := setup(t)
	work := t.TempDir()
	a := filepath.Join(work, "a")
	mustGit(t, work, "clone", e.url("x", "gst_alice", "/alice/priv.git"), a)
	if b, err := os.ReadFile(filepath.Join(a, "README.md")); err != nil || string(b) != "# ciao\n" {
		t.Fatalf("README non clonato: %q %v", b, err)
	}

	// Push di un nuovo commit con il token di alice.
	if err := os.WriteFile(filepath.Join(a, "f.txt"), []byte("uno\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, a, "add", ".")
	mustGit(t, a, "commit", "-m", "uno")
	mustGit(t, a, "push", "origin", "HEAD")

	// Un secondo clone vede il commit; poi pull dopo un altro push.
	b := filepath.Join(work, "b")
	mustGit(t, work, "clone", e.url("alice", "gst_alice", "/alice/priv.git"), b)
	if _, err := os.Stat(filepath.Join(b, "f.txt")); err != nil {
		t.Fatalf("il push non è arrivato: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a, "g.txt"), []byte("due\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, a, "add", ".")
	mustGit(t, a, "commit", "-m", "due")
	mustGit(t, a, "push", "origin", "HEAD")
	mustGit(t, b, "pull")
	if _, err := os.Stat(filepath.Join(b, "g.txt")); err != nil {
		t.Fatalf("il pull non ha portato g.txt: %v", err)
	}
}

func TestSenzaCredenziali401(t *testing.T) {
	e := setup(t)
	for _, p := range []string{
		"/alice/priv.git/info/refs?service=git-upload-pack",
		"/alice/priv.git/info/refs?service=git-receive-pack",
		"/nessuno/niente.git/info/refs?service=git-upload-pack", // inesistente: stesso 401
	} {
		resp, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, voglio 401", p, resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
			t.Errorf("%s: manca WWW-Authenticate Basic", p)
		}
	}
	work := t.TempDir()
	if out, err := git(t, work, "clone", e.srv.URL+"/alice/priv.git", "x"); err == nil {
		t.Fatalf("clone anonimo riuscito:\n%s", out)
	}
	// Token sconosciuto, scaduto o revocato: per identity è la stessa cosa.
	if out, err := git(t, work, "clone", e.url("x", "gst_scaduto", "/alice/priv.git"), "y"); err == nil {
		t.Fatalf("clone con token non valido riuscito:\n%s", out)
	} else if !strings.Contains(out, "401") && !strings.Contains(out, "Authentication failed") {
		t.Errorf("atteso 401 nell'errore di git:\n%s", out)
	}
}

func TestUtenteSenzaPermesso(t *testing.T) {
	e := setup(t)
	work := t.TempDir()
	// carol non ha grant su un repo privato: 404 identico a un repo che non esiste.
	statusOf := func(token, path string) int {
		req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
		req.SetBasicAuth("u", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if s := statusOf("gst_carol", "/alice/priv.git/info/refs?service=git-upload-pack"); s != 404 {
		t.Errorf("repo privato altrui: %d, voglio 404", s)
	}
	if s := statusOf("gst_carol", "/alice/inesistente.git/info/refs?service=git-upload-pack"); s != 404 {
		t.Errorf("repo inesistente: %d, voglio 404", s)
	}
	if s := statusOf("gst_alice", "/alice/gone.git/info/refs?service=git-upload-pack"); s != 404 {
		t.Errorf("repo nel cestino: %d, voglio 404", s)
	}
	if s := statusOf("gst_carol", "/alice/priv.git/info/refs?service=git-receive-pack"); s != 404 {
		t.Errorf("push su repo non leggibile: %d, voglio 404", s)
	}
	if out, err := git(t, work, "clone", e.url("carol", "gst_carol", "/alice/priv.git"), "c"); err == nil {
		t.Fatalf("clone senza permesso riuscito:\n%s", out)
	}
}

func TestSoloLetturaNonPuoPushare(t *testing.T) {
	e := setup(t)
	work := t.TempDir()
	// bob ha solo il ruolo read su priv: clone sì, push no (403).
	c := filepath.Join(work, "c")
	mustGit(t, work, "clone", e.url("bob", "gst_bob", "/alice/priv.git"), c)
	if err := os.WriteFile(filepath.Join(c, "x.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, c, "add", ".")
	mustGit(t, c, "commit", "-m", "x")
	out, err := git(t, c, "push", "origin", "HEAD")
	if err == nil {
		t.Fatalf("push con solo read riuscito:\n%s", out)
	}
	if !strings.Contains(out, "403") {
		t.Errorf("atteso 403 nell'errore di git:\n%s", out)
	}
	// Token di sola lettura (senza write:resource) anche per chi sarebbe admin.
	d := filepath.Join(work, "d")
	mustGit(t, work, "clone", e.url("alice", "gst_alice_ro", "/alice/priv.git"), d)
	if err := os.WriteFile(filepath.Join(d, "y.txt"), []byte("y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, d, "add", ".")
	mustGit(t, d, "commit", "-m", "y")
	if out, err := git(t, d, "push", "origin", "HEAD"); err == nil || !strings.Contains(out, "403") {
		t.Fatalf("push con token senza write:resource: err=%v\n%s", err, out)
	}
	// Il repo non è cambiato.
	dir, _ := e.store.RepoPath(repoPriv)
	if out := mustGit(t, dir, "rev-list", "--count", "HEAD"); strings.TrimSpace(out) != "1" {
		t.Errorf("il repo è cambiato: %s", out)
	}
}

func TestRepoArchiviatoSiLeggeMaNonSiScrive(t *testing.T) {
	e := setup(t)
	work := t.TempDir()
	c := filepath.Join(work, "c")
	mustGit(t, work, "clone", e.url("alice", "gst_alice", "/alice/arch.git"), c)
	mustGit(t, c, "commit", "--allow-empty", "-m", "vuoto")
	out, err := git(t, c, "push", "origin", "HEAD")
	if err == nil || !strings.Contains(out, "403") {
		t.Fatalf("push su repo archiviato: err=%v\n%s", err, out)
	}
	dir, _ := e.store.RepoPath(repoArch)
	if n := mustGit(t, dir, "rev-list", "--count", "HEAD"); strings.TrimSpace(n) != "1" {
		t.Errorf("il repo archiviato è cambiato: %s", n)
	}
}

func TestRepoInternoClonabileMaNonPushabile(t *testing.T) {
	e := setup(t)
	work := t.TempDir()
	// carol non ha grant, ma il repo è interno (P3).
	c := filepath.Join(work, "c")
	mustGit(t, work, "clone", e.url("carol", "gst_carol", "/alice/shared.git"), c)
	if err := os.WriteFile(filepath.Join(c, "z.txt"), []byte("z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, c, "add", ".")
	mustGit(t, c, "commit", "-m", "z")
	out, err := git(t, c, "push", "origin", "HEAD")
	if err == nil || !strings.Contains(out, "403") {
		t.Fatalf("push su repo interno senza write: err=%v\n%s", err, out)
	}
	// Il proprietario invece può.
	mustGit(t, c, "-c", "http.extraheader=", "push", e.url("alice", "gst_alice", "/alice/shared.git"), "HEAD:refs/heads/altro")
}

func TestDumbProtocolRifiutato(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/alice/priv.git/info/refs", nil)
	req.SetBasicAuth("u", "gst_alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("senza service: %d, voglio 403", resp.StatusCode)
	}
}
