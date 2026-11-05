//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0056 (GIT-530): niente più cestino. I repo che erano eliminati escono da core.repositories (con quello
// che si portano dietro) ed entrano nella coda repo_cleanup con owner/nome per il log; il nome torna
// libero; un repo vivo non si tocca. Il down toglie solo la coda.
func TestMigration0056_RepoNelCestinoInCodaENomeLibero(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	if err := migrate.Down(ctx, pool, dsn, 5); err != nil {
		t.Fatalf("down 0056: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'repo_cleanup'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabella dopo il down: %d (%v)", n, err)
	}

	trashed := newIssueRepo(t, pool, "odin")
	live := newIssueRepo(t, pool, "live")
	mustExec(t, pool, `UPDATE core.repositories SET owner_name = 'admin', deleted_at = now() WHERE resource_id = $1`, trashed)
	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM core.repositories WHERE resource_id = $1`, trashed).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	issue := uuid.New()
	mustExec(t, pool, `INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 1, 't', $3)`, issue, trashed, uuid.New())

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0056: %v", err)
	}
	for _, c := range []struct {
		q    string
		args []any
		want int
	}{
		{`SELECT count(*) FROM core.repositories WHERE resource_id = $1`, []any{trashed}, 0},
		{`SELECT count(*) FROM core.resources WHERE id = $1`, []any{trashed}, 0},
		{`SELECT count(*) FROM core.issues WHERE id = $1`, []any{issue}, 0},
		{`SELECT count(*) FROM core.repositories WHERE resource_id = $1`, []any{live}, 1},
		{`SELECT count(*) FROM core.repo_cleanup`, nil, 1},
		{`SELECT count(*) FROM core.repo_cleanup WHERE repo_id = $1 AND owner_name = 'admin' AND name = 'odin' AND source = 'trash'`, []any{trashed}, 1},
	} {
		if err := pool.QueryRow(ctx, c.q, c.args...).Scan(&n); err != nil || n != c.want {
			t.Fatalf("%s: %d (%v), voluto %d", c.q, n, err, c.want)
		}
	}
	// Il nome occupato dal cestino ora è libero.
	if err := insertRepo(pool, newResource(t, pool, "repo", "admin/odin"), ownerID, "odin"); err != nil {
		t.Fatalf("il nome doveva essere libero: %v", err)
	}

	if err := migrate.Down(ctx, pool, dsn, 5); err != nil {
		t.Fatalf("down 0056 (di nuovo): %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.repositories WHERE resource_id = $1`, live).Scan(&n); err != nil || n != 1 {
		t.Fatalf("repo vivo dopo il down: %d (%v)", n, err)
	}
}
