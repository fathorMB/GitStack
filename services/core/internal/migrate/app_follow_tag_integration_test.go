//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0047_AppFollowTag (GIT-377, H7): la regola sui tag è una colonna a parte, mai insieme
// al branch; il trigger tag_push entra nel vincolo e il down lo riporta a manual.
func TestMigration0047_AppFollowTag(t *testing.T) {
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
	insertApp := func(id uuid.UUID, name string, branch, pattern any) error {
		return exec(`INSERT INTO core.apps (id, repo_id, name, follow_branch, follow_tag_pattern, guaranteed_cpu_millis, guaranteed_memory_bytes,
			limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, 100, 1000, 200, 2000, $6, $6)`,
			id, repo, name, branch, pattern, now)
	}
	byBranch, byTag := uuid.New(), uuid.New()
	if err := insertApp(byBranch, "web", "main", nil); err != nil {
		t.Fatalf("app sul branch: %v", err)
	}
	if err := insertApp(byTag, "api", nil, "v*-test.*"); err != nil {
		t.Fatalf("app sulla regola sui tag: %v", err)
	}
	if insertApp(uuid.New(), "both", "main", "v*") == nil {
		t.Error("branch e regola sui tag insieme accettati")
	}
	if insertApp(uuid.New(), "empty", nil, "") == nil {
		t.Error("regola vuota accettata")
	}
	if err := exec(`INSERT INTO core.app_versions (app_id, number, trigger, image_reference, image_source, config, created_at)
		VALUES ($1, 1, 'tag_push', 'h/a/r:v1.2-test.3', 'registry', '{}'::jsonb, $2)`, byTag, now); err != nil {
		t.Fatalf("trigger tag_push: %v", err)
	}
	if exec(`INSERT INTO core.app_versions (app_id, number, trigger, image_reference, image_source, config, created_at)
		VALUES ($1, 2, 'boh', 'x', 'registry', '{}'::jsonb, $2)`, byTag, now) == nil {
		t.Error("trigger non valido accettato")
	}
	if err := migrate.Down(ctx, pool, dsn, 8); err != nil {
		t.Fatalf("down 0047: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'apps' AND column_name = 'follow_tag_pattern'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0047: %d %v", n, err)
	}
	var trigger string
	if err := pool.QueryRow(ctx, `SELECT trigger FROM core.app_versions WHERE app_id = $1 AND number = 1`, byTag).Scan(&trigger); err != nil || trigger != "manual" {
		t.Fatalf("il down riporta tag_push a manual: %q %v", trigger, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
