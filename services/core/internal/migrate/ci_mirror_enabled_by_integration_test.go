//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0039_CIMirrorEnabledBy (GIT-374, CI3): ci_repo_state ha la colonna di chi ha acceso la
// CI sul mirror (nullable, le righe esistenti restano valide); il down la toglie e il nuovo up la rimette.
func TestMigration0039_CIMirrorEnabledBy(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	has := func() bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'core' AND table_name = 'ci_repo_state' AND column_name = 'run_on_pull_mirror_by' AND is_nullable = 'YES'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !has() {
		t.Fatal("run_on_pull_mirror_by manca dopo le migrazioni")
	}
	if err := migrate.Down(ctx, pool, dsn, 5); err != nil {
		t.Fatalf("down 0039: %v", err)
	}
	if has() {
		t.Fatal("run_on_pull_mirror_by resta dopo il down")
	}
	// Il resto di ci_repo_state resta (il down è solo della 0039).
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'ci_repo_state' AND column_name = 'run_on_pull_mirror'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("run_on_pull_mirror dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if !has() {
		t.Fatal("colonna assente dopo il nuovo up")
	}
}
