//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0020 (GIT-263): metodo di merge proposto per repo (default squash, sempre fra
// quelli ammessi) e tipo di evento branch_deleted. Il down toglie la colonna e
// ripulisce gli eventi nuovi; l'up li riporta.
func TestMigration0020_MetodoProposto(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "defmethod")

	mustExec(t, pool, `INSERT INTO core.branch_settings (repo_id) VALUES ($1)`, repo)
	var def string
	if err := pool.QueryRow(ctx, `SELECT default_merge_method FROM core.branch_settings WHERE repo_id = $1`, repo).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if def != "squash" {
		t.Fatalf("default = %q, voluto squash", def)
	}
	// Valore sconosciuto, o non fra i metodi ammessi: rifiutato.
	_, err := pool.Exec(ctx, `UPDATE core.branch_settings SET default_merge_method = 'rebase' WHERE repo_id = $1`, repo)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.branch_settings SET merge_methods = ARRAY['merge'] WHERE repo_id = $1`, repo)
	wantCode(t, err, "23514")
	mustExec(t, pool, `UPDATE core.branch_settings SET merge_methods = ARRAY['merge'], default_merge_method = 'merge' WHERE repo_id = $1`, repo)

	tx, _ := pool.Begin(ctx)
	if _, err := insertPR(ctx, tx, repo); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var pr uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM core.pull_requests WHERE repo_id = $1`, repo).Scan(&pr); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO core.pull_request_events (id, pull_request_id, type) VALUES ($1, $2, 'branch_deleted')`, uuid.New(), pr)

	if err := migrate.Down(ctx, pool, dsn, 17); err != nil {
		t.Fatalf("down 0020: %v", err)
	}
	var cols, events int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM information_schema.columns WHERE table_schema='core' AND table_name='branch_settings' AND column_name='default_merge_method'),
		(SELECT count(*) FROM core.pull_request_events WHERE type = 'branch_deleted')`).Scan(&cols, &events); err != nil {
		t.Fatal(err)
	}
	if cols != 0 || events != 0 {
		t.Fatalf("dopo il down: colonne=%d eventi=%d", cols, events)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_events (id, pull_request_id, type) VALUES ($1, $2, 'branch_deleted')`, uuid.New(), pr)
	wantCode(t, err, "23514")
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0020: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT default_merge_method FROM core.branch_settings WHERE repo_id = $1`, repo).Scan(&def); err != nil || def != "merge" {
		t.Fatalf("dopo il re-up (solo merge ammesso): %q, %v", def, err)
	}
}
