package sshd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Le regole alla ricezione del push (R6, R9) sono le stesse dello smart HTTP:
// qui si prova che valgono anche via SSH, con git e ssh reali.

func sshClone(t *testing.T, e *env) string {
	t.Helper()
	work := t.TempDir()
	if out, err := e.gitCmd(t, e.rw, work, "clone", e.url("/alice/app.git"), "app"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	return filepath.Join(work, "app")
}

func TestPushFileOltreLaSogliaViaSSH(t *testing.T) {
	e := start(t)
	app := sshClone(t, e)
	const limit = 100 << 20

	f, err := os.Create(filepath.Join(app, "ok.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(limit); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustGit(t, app, "add", ".")
	mustGit(t, app, "commit", "-m", "alla soglia")
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD"); err != nil {
		t.Fatalf("push alla soglia: %v\n%s", err, out)
	}

	f, err = os.Create(filepath.Join(app, "grande.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(limit + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustGit(t, app, "add", ".")
	mustGit(t, app, "commit", "-m", "troppo grande")
	out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD")
	if err == nil {
		t.Fatalf("il push doveva fallire:\n%s", out)
	}
	for _, want := range []string{"remote: gitstack: push rifiutato", "grande.bin", "104857601 byte"} {
		if !strings.Contains(out, want) {
			t.Errorf("manca %q:\n%s", want, out)
		}
	}
}

func TestBranchPrincipaleProtettoViaSSH(t *testing.T) {
	e := start(t)
	app := sshClone(t, e)
	mustGit(t, app, "commit", "--allow-empty", "-m", "due")
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	// Altri branch liberi.
	for _, args := range [][]string{
		{"push", "origin", "HEAD:refs/heads/feature"},
		{"push", "origin", "--delete", "feature"},
	} {
		if out, err := e.gitCmd(t, e.rw, app, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit(t, app, "commit", "--amend", "--allow-empty", "-m", "riscritto")
	out, err := e.gitCmd(t, e.rw, app, "push", "--force", "origin", "HEAD:refs/heads/main")
	if err == nil || !strings.Contains(out, "remote: gitstack: push rifiutato") || !strings.Contains(out, "force-push") {
		t.Fatalf("force-push: err=%v\n%s", err, out)
	}
	out, err = e.gitCmd(t, e.rw, app, "push", "origin", "--delete", "main")
	if err == nil || !strings.Contains(out, "non si può eliminare") {
		t.Fatalf("eliminazione: err=%v\n%s", err, out)
	}

	// Protezione spenta: entrambi accettati.
	e.dir.unprotected.Store(true)
	if out, err := e.gitCmd(t, e.rw, app, "push", "--force", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("force-push senza protezione: %v\n%s", err, out)
	}
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "--delete", "main"); err != nil {
		t.Fatalf("eliminazione senza protezione: %v\n%s", err, out)
	}
}
