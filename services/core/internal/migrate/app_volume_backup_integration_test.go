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

// TestMigration0045_AppVolumeBackup (GIT-357, H3, D19): i volumi esistenti entrano nel backup di default
// (colonna true), l'esclusione si può scrivere, il down toglie la colonna e i volumi restano.
func TestMigration0045_AppVolumeBackup(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	repo, owner, app := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	for _, step := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, []any{repo}},
		{`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, []any{repo, owner}},
		{`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
			limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, []any{app, repo, now}},
		{`INSERT INTO core.app_volumes (app_id, name, mount_path, size_bytes, version, created_at) VALUES ($1, 'data', '/data', 1048576, 2, $2)`, []any{app, now}},
	} {
		if err := exec(step.q, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	var in bool
	if err := pool.QueryRow(ctx, `SELECT include_in_backup FROM core.app_volumes WHERE name = 'data'`).Scan(&in); err != nil || !in {
		t.Fatalf("un volume nuovo deve essere nel backup di default: %v %v", in, err)
	}
	if err := exec(`UPDATE core.app_volumes SET include_in_backup = false WHERE name = 'data'`); err != nil {
		t.Fatal(err)
	}
	if exec(`UPDATE core.app_volumes SET include_in_backup = NULL WHERE name = 'data'`) == nil {
		t.Error("include_in_backup non può essere NULL")
	}
	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0045: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_volumes' AND column_name = 'include_in_backup'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0045: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_volumes`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("i volumi restano dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT include_in_backup FROM core.app_volumes WHERE name = 'data'`).Scan(&in); err != nil || !in {
		t.Fatalf("dopo il nuovo up il volume è di nuovo nel backup di default: %v %v", in, err)
	}
}
