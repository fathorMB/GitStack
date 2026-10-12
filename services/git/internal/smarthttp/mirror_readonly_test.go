package smarthttp_test

import (
	"strings"
	"testing"
)

// MP2: un repo mirror in pull è in sola lettura via HTTPS, con un messaggio
// che cita l'origine e il codice documentato; dopo «Interrompi» (MP6) il push
// torna possibile subito.
func TestMirrorInPullRifiutaIlPush(t *testing.T) {
	e := setup(t)
	w := cloneAlice(t, e)
	mustGit(t, w, "commit", "--allow-empty", "-m", "locale")

	const origin = "https://github.com/example/app.git"
	e.core.setMirror(repoPriv, origin)

	for _, args := range [][]string{
		{"push", "origin", "HEAD"},
		{"push", "origin", "HEAD:refs/heads/feature"},
		{"push", "origin", "--delete", "main"},
	} {
		out, err := git(t, w, args...)
		if err == nil {
			t.Fatalf("git %v su un mirror doveva fallire:\n%s", args, out)
		}
		for _, want := range []string{
			"remote: gitstack: push rifiutato",
			"questo repo è una copia di " + origin + " (mirror in pull): il codice si cambia all'origine",
			"mirror_read_only",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("git %v: manca %q:\n%s", args, want, out)
			}
		}
	}
	// Nessun ref è cambiato.
	dir, _ := e.store.RepoPath(repoPriv)
	if n := strings.TrimSpace(mustGit(t, dir, "rev-list", "--count", "main")); n != "1" {
		t.Errorf("main ha %s commit, atteso 1", n)
	}
	// La lettura resta libera.
	mustGit(t, w, "fetch", "origin")

	// Interrompi: il push riesce subito.
	e.core.setMirror(repoPriv, "")
	if out, err := git(t, w, "push", "origin", "HEAD"); err != nil {
		t.Fatalf("dopo Interrompi il push doveva riuscire: %v\n%s", err, out)
	}
	if n := strings.TrimSpace(mustGit(t, dir, "rev-list", "--count", "main")); n != "2" {
		t.Errorf("main ha %s commit, attesi 2", n)
	}
}
