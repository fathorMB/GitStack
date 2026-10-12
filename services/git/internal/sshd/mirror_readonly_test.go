package sshd

import (
	"strings"
	"testing"
)

// MP2: un repo mirror in pull rifiuta il push anche via SSH, con l'origine nel
// messaggio; dopo «Interrompi» (MP6) il push riesce.
func TestMirrorInPullRifiutaIlPushViaSSH(t *testing.T) {
	e := start(t)
	app := sshClone(t, e)
	mustGit(t, app, "commit", "--allow-empty", "-m", "locale")

	const origin = "https://github.com/example/app.git"
	e.dir.mirrorOrigin.Store(origin)
	out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD")
	if err == nil {
		t.Fatalf("il push su un mirror doveva fallire:\n%s", out)
	}
	for _, want := range []string{
		"remote: gitstack: push rifiutato",
		"questo repo è una copia di " + origin + " (mirror in pull): il codice si cambia all'origine",
		"mirror_read_only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manca %q:\n%s", want, out)
		}
	}
	// La lettura resta libera.
	if out, err := e.gitCmd(t, e.rw, app, "fetch", "origin"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}

	e.dir.mirrorOrigin.Store("")
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD"); err != nil {
		t.Fatalf("dopo Interrompi il push doveva riuscire: %v\n%s", err, out)
	}
}
