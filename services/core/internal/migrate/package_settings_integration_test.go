//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigration0032_ImpostazioniDeiPacchetti (GIT-325): default di RG2, vincoli sui valori,
// cascata all'eliminazione del repo; il down toglie la tabella e il nuovo up rifunziona.
func TestMigration0032_ImpostazioniDeiPacchetti(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	repo, user := uuid.New(), uuid.New()
	for _, q := range []string{
		`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`,
	} {
		if err := exec(q, repo); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, user); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repo_package_settings (repo_id) VALUES ($1)`, repo); err != nil {
		t.Fatal(err)
	}
	var days int
	var backup bool
	var keep *int
	if err := pool.QueryRow(ctx, `SELECT delete_untagged_after_days, include_in_backup, keep_last_tags FROM core.repo_package_settings WHERE repo_id = $1`, repo).Scan(&days, &backup, &keep); err != nil || days != 7 || backup || keep != nil {
		t.Fatalf("default: %d %v %v %v", days, backup, keep, err)
	}
	if exec(`UPDATE core.repo_package_settings SET delete_untagged_after_days = 0`) == nil {
		t.Error("deleteUntaggedAfterDays >= 1")
	}
	if exec(`UPDATE core.repo_package_settings SET keep_last_tags = 0`) == nil {
		t.Error("keepLastTags >= 1")
	}
	if exec(`UPDATE core.repo_package_settings SET delete_sha_tags_after_days = 0`) == nil {
		t.Error("deleteShaTagsAfterDays >= 1")
	}
	if exec(`INSERT INTO core.repo_package_settings (repo_id) VALUES ($1)`, uuid.New()) == nil {
		t.Error("il repo deve esistere")
	}
	if err := exec(`DELETE FROM core.repositories WHERE resource_id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_package_settings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("righe dopo l'eliminazione del repo: %d %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 14); err != nil {
		t.Fatalf("down 0032: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'repo_package_settings'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabella dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
