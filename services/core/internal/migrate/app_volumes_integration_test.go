//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigration0042_AppVolumes (GIT-356, H3): volumi delle app con nome e percorso unici per app,
// dimensione positiva, cascata dall'app; il down toglie la tabella e il nuovo up rifunziona.
func TestMigration0042_AppVolumes(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	repo, owner := uuid.New(), uuid.New()
	if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	app := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, app, repo, now); err != nil {
		t.Fatal(err)
	}
	ins := func(name, path string, size int64) error {
		return exec(`INSERT INTO core.app_volumes (app_id, name, mount_path, size_bytes, version, created_at) VALUES ($1, $2, $3, $4, 2, $5)`, app, name, path, size, now)
	}
	if err := ins("data", "/data", 1<<30); err != nil {
		t.Fatal(err)
	}
	if ins("data", "/altro", 1<<30) == nil {
		t.Error("il nome del volume è unico per app")
	}
	if ins("altro", "/data", 1<<30) == nil {
		t.Error("il percorso di mount è unico per app")
	}
	if ins("zero", "/zero", 0) == nil {
		t.Error("la dimensione deve essere positiva")
	}
	if ins("Maiuscola", "/m", 1) == nil {
		t.Error("il nome è un'etichetta DNS in minuscolo")
	}
	if ins("relativo", "relativo", 1) == nil {
		t.Error("il percorso di mount è assoluto")
	}
	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_volumes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dopo l'eliminazione dell'app restano %d volumi: %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0042: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_volumes'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella dei volumi deve sparire con il down 0042: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'apps'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("la tabella apps deve restare: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_volumes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella dei volumi dopo l'up: %d %v", n, err)
	}
}
