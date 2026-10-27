//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0019 (GIT-261): cronologia delle PR, origine via token e una sola PR aperta
// per coppia di branch. Il down toglie tabella, colonne e indice senza
// toccare le PR; l'up li riporta.
func TestMigration0019_EventiEUnicitaDelleAperte(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "events")

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
	mustExec(t, pool, `UPDATE core.pull_requests SET source_branch = 'feat' WHERE id = $1`, pr)

	// Eventi: tipi ammessi, cancellati con la PR.
	mustExec(t, pool, `INSERT INTO core.pull_request_events (id, pull_request_id, type, actor_id, data)
		VALUES ($1, $2, 'opened', $3, '{"draft":false}')`, uuid.New(), pr, uuid.New())
	_, err := pool.Exec(ctx, `INSERT INTO core.pull_request_events (id, pull_request_id, type) VALUES ($1, $2, 'boh')`, uuid.New(), pr)
	wantCode(t, err, "23514")
	mustExec(t, pool, `UPDATE core.pull_requests SET via_token_id = $2, via_token_name = 'ci' WHERE id = $1`, pr, uuid.New())

	// Una sola PR aperta per coppia di branch; chiusa, la coppia si libera.
	n := func() int64 {
		tx, _ := pool.Begin(ctx)
		defer func() { _ = tx.Rollback(ctx) }()
		var num int64
		if err := tx.QueryRow(ctx, takeNumber, repo).Scan(&num); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO core.pull_requests (id, repo_id, number, source_branch, target_branch, title, author_id)
			VALUES ($1, $2, $3, 'feat', 'main', 't', $4)`, uuid.New(), repo, num, uuid.New())
		wantCode(t, err, "23505")
		return num
	}
	n()
	mustExec(t, pool, `UPDATE core.pull_requests SET state = 'closed' WHERE id = $1`, pr)
	tx, _ = pool.Begin(ctx)
	var num int64
	if err := tx.QueryRow(ctx, takeNumber, repo).Scan(&num); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.pull_requests (id, repo_id, number, source_branch, target_branch, title, author_id)
		VALUES ($1, $2, $3, 'feat', 'main', 't', $4)`, uuid.New(), repo, num, uuid.New()); err != nil {
		t.Fatalf("la coppia di una PR chiusa deve essere libera: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Giù: via tabella, colonne e indice; le PR restano.
	if err := migrate.Down(ctx, pool, dsn, 26); err != nil {
		t.Fatalf("down 0019: %v", err)
	}
	if tableExists(t, pool, "pull_request_events") {
		t.Fatal("pull_request_events non dovrebbe esistere dopo il down")
	}
	var prs, cols, idx int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.pull_requests),
		(SELECT count(*) FROM information_schema.columns WHERE table_schema='core' AND table_name='pull_requests' AND column_name LIKE 'via_token%'),
		(SELECT count(*) FROM pg_indexes WHERE schemaname='core' AND indexname='pull_requests_open_branches_key')`).Scan(&prs, &cols, &idx); err != nil {
		t.Fatal(err)
	}
	if prs != 2 || cols != 0 || idx != 0 {
		t.Fatalf("dopo il down: prs=%d colonne=%d indici=%d", prs, cols, idx)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0019: %v", err)
	}
	if !tableExists(t, pool, "pull_request_events") {
		t.Fatal("pull_request_events manca dopo l'up")
	}
}
