package repo_test

import (
	"strings"
	"testing"
)

// MP7: gs repo view dice che il repo è un mirror in pull, con origine e
// ultimo aggiornamento; in un repo normale la riga non c'è.
func TestView_Mirror(t *testing.T) {
	mirror := strings.Replace(repoJSON, `"createdAt"`,
		`"mirror":{"origin":"https://github.com/example/web.git","lastSyncedAt":"2026-10-12T09:40:00Z"},"createdAt"`, 1)
	fk := newFake(t, map[string]reply{"GET /repos/acme/web": {200, mirror}})
	r := run(t, fk, "repo", "view", "acme/web")
	want := "Mirror di https://github.com/example/web.git, aggiornato 2026-10-12 09:40 UTC"
	if r.code != 0 || !strings.Contains(r.out, want) {
		t.Errorf("exit %d, manca %q: %s | %s", r.code, want, r.out, r.errOut)
	}

	// Prima copia non ancora finita.
	first := strings.Replace(mirror, `,"lastSyncedAt":"2026-10-12T09:40:00Z"`, "", 1)
	fk = newFake(t, map[string]reply{"GET /repos/acme/web": {200, first}})
	r = run(t, fk, "repo", "view", "acme/web")
	if r.code != 0 || !strings.Contains(r.out, "Mirror di https://github.com/example/web.git, aggiornato mai") {
		t.Errorf("prima copia: exit %d: %s", r.code, r.out)
	}

	// --json: il campo mirror c'è.
	r = run(t, fk, "repo", "view", "acme/web", "--json", "mirror")
	if r.code != 0 || !strings.Contains(r.out, `"origin"`) || !strings.Contains(r.out, "github.com/example/web.git") {
		t.Errorf("--json mirror: exit %d: %s", r.code, r.out)
	}

	// Repo normale: nessuna riga.
	fk = newFake(t, map[string]reply{"GET /repos/acme/web": {200, repoJSON}})
	r = run(t, fk, "repo", "view", "acme/web")
	if r.code != 0 || strings.Contains(r.out, "Mirror di") {
		t.Errorf("repo normale: exit %d: %s", r.code, r.out)
	}
}
