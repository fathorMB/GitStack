package smarthttp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
)

// cloneAlice clona alice/priv e ritorna la cartella di lavoro.
func cloneAlice(t *testing.T, e *env) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "w")
	mustGit(t, filepath.Dir(work), "clone", e.url("alice", "gst_alice", "/alice/priv.git"), work)
	return work
}

// writeSized scrive un file di n byte (zeri: si comprime, il blob resta di n).
func writeSized(t *testing.T, path string, n int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(n); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPushFileOltreLaSogliaRifiutato(t *testing.T) {
	e := setup(t)
	w := cloneAlice(t, e)
	const limit = 100 << 20

	// Un file piccolo passa.
	if err := os.WriteFile(filepath.Join(w, "piccolo.txt"), []byte("ciao\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, w, "add", ".")
	mustGit(t, w, "commit", "-m", "piccolo")
	mustGit(t, w, "push", "origin", "HEAD")

	// Esattamente alla soglia: passa.
	writeSized(t, filepath.Join(w, "limite.bin"), limit)
	mustGit(t, w, "add", ".")
	mustGit(t, w, "commit", "-m", "alla soglia")
	mustGit(t, w, "push", "origin", "HEAD")

	// Soglia + 1 byte: rifiutato, con file e dimensione nel messaggio.
	if err := os.MkdirAll(filepath.Join(w, "dir con spazi"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(w, "dir con spazi", "grande.bin"), limit+1)
	mustGit(t, w, "add", ".")
	mustGit(t, w, "commit", "-m", "troppo grande")
	out, err := git(t, w, "push", "origin", "HEAD")
	if err == nil {
		t.Fatalf("il push di un file oltre la soglia doveva fallire:\n%s", out)
	}
	for _, want := range []string{"remote: gitstack: push rifiutato", "dir con spazi/grande.bin", "104857601 byte", "limite di 100.0 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("manca %q nel messaggio:\n%s", want, out)
		}
	}
	// Il repo non ha ricevuto il commit.
	dir, _ := e.store.RepoPath(repoPriv)
	if n := strings.TrimSpace(mustGit(t, dir, "rev-list", "--count", "main")); n != "3" {
		t.Errorf("il repo ha %s commit, attesi 3 (il push rifiutato non doveva entrare)", n)
	}

	// Tolto il file dalla storia il push passa.
	mustGit(t, w, "reset", "--hard", "HEAD~1")
	mustGit(t, w, "commit", "--allow-empty", "-m", "ok")
	mustGit(t, w, "push", "origin", "HEAD")
}

func TestPushSogliaConfigurabile(t *testing.T) {
	e := setupWith(t, receiverules.Limits{MaxBlobBytes: 1 << 10})
	w := cloneAlice(t, e)
	writeSized(t, filepath.Join(w, "due.bin"), 2<<10)
	mustGit(t, w, "add", ".")
	mustGit(t, w, "commit", "-m", "due kb")
	out, err := git(t, w, "push", "origin", "HEAD")
	if err == nil || !strings.Contains(out, "due.bin") || !strings.Contains(out, "2048 byte") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	// Soglia a zero: nessun limite.
	e = setupWith(t, receiverules.Limits{})
	w = cloneAlice(t, e)
	writeSized(t, filepath.Join(w, "due.bin"), 2<<10)
	mustGit(t, w, "add", ".")
	mustGit(t, w, "commit", "-m", "due kb")
	mustGit(t, w, "push", "origin", "HEAD")
}

func TestPushRepoGrandeAvvisaMaAccetta(t *testing.T) {
	e := setupWith(t, receiverules.Limits{MaxBlobBytes: 100 << 20, WarnRepoBytes: 1 << 10})
	w := cloneAlice(t, e)
	mustGit(t, w, "commit", "--allow-empty", "-m", "vuoto")
	out, err := git(t, w, "push", "origin", "HEAD")
	if err != nil {
		t.Fatalf("il push doveva passare con un avviso: %v\n%s", err, out)
	}
	if !strings.Contains(out, "remote: gitstack: avviso") {
		t.Errorf("manca l'avviso:\n%s", out)
	}
}

func TestBranchPrincipaleProtetto(t *testing.T) {
	e := setup(t)
	w := cloneAlice(t, e)
	mustGit(t, w, "commit", "--allow-empty", "-m", "due")
	mustGit(t, w, "push", "origin", "HEAD")

	// Un branch qualunque è libero: push, force-push ed eliminazione.
	mustGit(t, w, "push", "origin", "HEAD:refs/heads/feature")
	mustGit(t, w, "commit", "--amend", "--allow-empty", "-m", "riscritto")
	mustGit(t, w, "push", "--force", "origin", "HEAD:refs/heads/feature")
	mustGit(t, w, "push", "origin", "--delete", "feature")

	// Force-push su main rifiutato.
	out, err := git(t, w, "push", "--force", "origin", "HEAD:refs/heads/main")
	if err == nil || !strings.Contains(out, "remote: gitstack: push rifiutato") || !strings.Contains(out, "force-push") {
		t.Fatalf("force-push su main: err=%v\n%s", err, out)
	}
	// Eliminazione di main rifiutata.
	out, err = git(t, w, "push", "origin", "--delete", "main")
	if err == nil || !strings.Contains(out, "remote: gitstack: push rifiutato") || !strings.Contains(out, "non si può eliminare") {
		t.Fatalf("eliminazione di main: err=%v\n%s", err, out)
	}
	dir, _ := e.store.RepoPath(repoPriv)
	if n := strings.TrimSpace(mustGit(t, dir, "rev-list", "--count", "main")); n != "2" {
		t.Errorf("main ha %s commit, attesi 2", n)
	}

	// Un push normale (fast-forward) su main resta possibile.
	mustGit(t, w, "reset", "--hard", "origin/main")
	mustGit(t, w, "commit", "--allow-empty", "-m", "tre")
	mustGit(t, w, "push", "origin", "HEAD:refs/heads/main")
}

func TestBranchPrincipaleSenzaProtezione(t *testing.T) {
	e := setup(t)
	e.core.setUnprotected(repoPriv)
	w := cloneAlice(t, e)
	mustGit(t, w, "commit", "--amend", "--allow-empty", "-m", "riscritto")
	mustGit(t, w, "push", "--force", "origin", "HEAD:refs/heads/main")
	dir, _ := e.store.RepoPath(repoPriv)
	if s := strings.TrimSpace(mustGit(t, dir, "log", "-1", "--format=%s", "main")); s != "riscritto" {
		t.Fatalf("il force-push non è passato: %q", s)
	}
	mustGit(t, w, "push", "origin", "--delete", "main")
	if out, err := git(t, dir, "rev-parse", "--verify", "refs/heads/main"); err == nil {
		t.Fatalf("main doveva essere eliminato: %s", out)
	}
}
