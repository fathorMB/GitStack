//go:build integration

package ci_test

import (
	"context"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/google/uuid"
)

// GIT-374 (CI3 del 2026-10-26): le sincronizzazioni di un mirror in pull avviano i
// workflow solo con la CI accesa; l'autore è chi l'ha accesa e il suo tetto vale per il
// token del job; se perde admin la CI si spegne. Che un ref `diverged` non entri
// nell'evento git.push lo prova il servizio git (mirrorpull_test.go, TestPull_Divergente*).
func TestMirrorCI_EventiAutoreERevocaAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	creator, admin := uuid.New(), uuid.New()
	var mu sync.Mutex
	isAdmin := map[uuid.UUID]bool{admin: true}
	asked := map[uuid.UUID]bool{}
	e.eng.IsRepoAdmin = func(_ context.Context, u, _ uuid.UUID) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return isAdmin[u], nil
	}
	e.eng.ActorLevel = func(_ context.Context, actor, _ uuid.UUID) (bool, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		asked[actor] = true
		return true, false, nil // read soltanto
	}
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:\n    tags: ['**']\n    branches: ['**']")}

	repo := e.repo(uuid.New(), "mirror")
	e.exec(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, 'https://example.org/o/r.git', $3)`,
		uuid.New(), repo, creator)
	// Una sincronizzazione con un branch e un tag nuovi: l'evento ha due ref.
	sync2 := func() ci.Push {
		return ci.Push{RepoID: repo, Pusher: creator, Refs: []ci.PushRef{
			{Ref: "refs/heads/nuovo", Before: ci.ZeroSHA, After: sha("a"), Message: "x"},
			{Ref: "refs/tags/v1", Before: ci.ZeroSHA, After: sha("a"), Message: "x"},
		}}
	}

	// CI spenta (default): nessun run.
	if ids, err := e.eng.OnPush(ctx, sync2()); err != nil || len(ids) != 0 {
		t.Fatalf("CI spenta: %v %v", ids, err)
	}
	// Accesa da admin: un run per ref, autore = admin (non chi ha creato il mirror).
	if err := ci.SetMirrorCI(ctx, e.pool, repo, true, admin); err != nil {
		t.Fatal(err)
	}
	ids, err := e.eng.OnPush(ctx, sync2())
	if err != nil || len(ids) != 2 {
		t.Fatalf("CI accesa: %v %v", ids, err)
	}
	refs := map[string]bool{}
	for _, r := range e.runs(repo) {
		refs[r.Ref] = true
		if r.ActorID == nil || *r.ActorID != admin {
			t.Errorf("autore del run %s: %v, atteso %s", r.Ref, r.ActorID, admin)
		}
		for _, j := range r.Jobs {
			for k, v := range j.Permissions {
				if v == ci.LevelWrite {
					t.Errorf("job %s: %s=%s oltre i permessi di chi ha acceso la CI (read)", j.Name, k, v)
				}
			}
		}
	}
	if !refs["refs/heads/nuovo"] || !refs["refs/tags/v1"] {
		t.Errorf("ref avviati: %v", refs)
	}
	if !asked[admin] || asked[creator] {
		t.Errorf("il tetto del token segue chi ha acceso la CI: admin=%v creatore=%v", asked[admin], asked[creator])
	}

	// Tolto admin: la sincronizzazione successiva non avvia niente, la CI risulta spenta e chi l'aveva accesa è avvisato.
	mu.Lock()
	isAdmin[admin] = false
	mu.Unlock()
	e.src.files[sha("b")] = e.src.files[sha("a")]
	if ids, err := e.eng.OnPush(ctx, pushOf(repo, creator, "refs/heads/nuovo", sha("a"), sha("b"), "y")); err != nil || len(ids) != 0 {
		t.Fatalf("dopo la revoca: %v %v", ids, err)
	}
	m, err := ci.LoadMirrorCI(ctx, e.pool, repo)
	if err != nil || m.Enabled || m.EnabledBy != uuid.Nil {
		t.Fatalf("stato dopo la revoca: %+v %v", m, err)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE user_id = $1 AND event_name = $2 AND repo_id = $3`,
		admin, ci.EventMirrorCIDisabled, repo).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifiche di CI spenta: %d %v", n, err)
	}
	// L'avvio manuale è rifiutato come su un mirror spento.
	if _, err := e.eng.Dispatch(ctx, ci.Dispatch{RepoID: repo, Workflow: "ci.yml", Ref: "refs/heads/nuovo", SHA: sha("a"), Actor: admin}); err == nil {
		t.Error("dispatch con CI spenta")
	}
}
