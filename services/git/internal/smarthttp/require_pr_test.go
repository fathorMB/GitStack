package smarthttp_test

import (
	"strings"
	"testing"
)

// PR4: con «Richiedi una Pull Request» accesa il push diretto sul branch
// principale è rifiutato anche ad alice, che è admin del repo, con un
// messaggio chiaro; gli altri branch restano liberi; spenta l'impostazione, il
// push torna accettato.
func TestRichiediPullRequestRifiutaIlPushDirettoAncheAdmin(t *testing.T) {
	e := setup(t)
	w := cloneAlice(t, e)
	dir, _ := e.store.RepoPath(repoPriv)
	count := func() string { return strings.TrimSpace(mustGit(t, dir, "rev-list", "--count", "main")) }
	mustGit(t, w, "commit", "--allow-empty", "-m", "uno")

	e.core.setRequirePR(repoPriv, true)
	for _, args := range [][]string{
		{"push", "origin", "HEAD:refs/heads/main"},
		{"push", "--force", "origin", "HEAD:refs/heads/main"},
		{"push", "origin", "--delete", "main"},
	} {
		out, err := git(t, w, args...)
		if err == nil {
			t.Fatalf("git %v con l'impostazione accesa doveva fallire:\n%s", args, out)
		}
		for _, want := range []string{
			"remote: gitstack: push rifiutato",
			"«Richiedi una Pull Request»",
			"nemmeno per chi ha il ruolo admin",
			"pull_request_required",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("git %v: manca %q:\n%s", args, want, out)
			}
		}
	}
	if n := count(); n != "1" {
		t.Fatalf("main ha %s commit, atteso 1 (nessun push doveva passare)", n)
	}
	// Gli altri branch restano liberi: è la strada per aprire una PR.
	mustGit(t, w, "push", "origin", "HEAD:refs/heads/feature")

	// Spenta l'impostazione, il push diretto è accettato.
	e.core.setRequirePR(repoPriv, false)
	mustGit(t, w, "push", "origin", "HEAD:refs/heads/main")
	if n := count(); n != "2" {
		t.Errorf("main ha %s commit, attesi 2", n)
	}
}

// PR4 e MP: il merge di una PR e il fetch del mirror in pull scrivono i ref
// con update-ref nel repo, non con receive-pack, e quindi non passano
// dall'hook pre-receive: con l'impostazione accesa il merge riesce, mentre il
// push dello stesso utente è rifiutato. Il mirror in push (V8) è un push che
// parte da questo repo verso un altro, e non è toccato dall'hook di questo.
func TestRichiediPullRequestNonFermaMergeMirrorPullEMirrorPush(t *testing.T) {
	e := setup(t)
	w := cloneAlice(t, e)
	dir, _ := e.store.RepoPath(repoPriv)
	e.core.setRequirePR(repoPriv, true)

	// Un branch di feature con un commit, spinto: libero.
	mustGit(t, w, "checkout", "-b", "feature")
	mustGit(t, w, "commit", "--allow-empty", "-m", "feature")
	mustGit(t, w, "push", "origin", "feature")
	head := strings.TrimSpace(mustGit(t, w, "rev-parse", "HEAD"))
	if out, err := git(t, w, "push", "origin", "HEAD:refs/heads/main"); err == nil {
		t.Fatalf("il push diretto doveva fallire:\n%s", out)
	}

	// Come il merge di GIT-260 e il fetch di mirrorpull: update-ref nel repo.
	mustGit(t, dir, "update-ref", "-m", "merge", "refs/heads/main", head)
	if got := strings.TrimSpace(mustGit(t, dir, "rev-parse", "main")); got != head {
		t.Fatalf("main = %s, atteso %s", got, head)
	}

	// Mirror in push: il repo che spinge è questo, la destinazione è un altro
	// repo che non ha le regole di questo.
	dest := t.TempDir()
	mustGit(t, dest, "init", "--bare", "-b", "main")
	mustGit(t, dir, "push", dest, "main")
	if got := strings.TrimSpace(mustGit(t, dest, "rev-parse", "main")); got != head {
		t.Fatalf("la destinazione ha main = %s, atteso %s", got, head)
	}
}
