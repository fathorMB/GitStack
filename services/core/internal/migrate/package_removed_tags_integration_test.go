//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// TestMigration0035_TagRimossiDalRegistry verifica registro, ordine e cascata sul repo.
func TestMigration0035_TagRimossiDalRegistry(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range store.RecentRemovedTagsPerImage + 3 {
		if _, err := pool.Exec(ctx, `INSERT INTO core.repo_package_removed_tags (repo_id, image, name, digest, rule, removed_at)
			VALUES ($1, 'api', $2, 'sha256:aa', 'after 14 days', $3)`, repo, "sha-"+string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.New(pool).ListRemovedPackageTags(ctx, repo)
	if err != nil || len(got) != store.RecentRemovedTagsPerImage || got[0].Name != "sha-w" {
		t.Fatalf("tag recenti: %+v %v", got, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_package_removed_tags WHERE repo_id = $1`, repo).Scan(&count); err != nil || count != store.RecentRemovedTagsPerImage+3 {
		t.Fatalf("registro: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM core.repositories WHERE resource_id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_package_removed_tags`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascata: %d %v", count, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 16); err != nil {
		t.Fatalf("down 0035: %v", err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatal(err)
	}
}
