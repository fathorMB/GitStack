//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0021 (GIT-300): commenti generali delle PR (thread_id nullable,
// pull_request_id NOT NULL) e segnaposto dei commenti eliminati. Il down con
// commenti generali e segnaposto presenti li toglie/riscrive e rimette
// NOT NULL su thread_id; i commenti sulle righe sopravvivono.
func TestMigration0021_CommentiGenerali(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "general")
	user := uuid.New()

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

	th, reply, general, placeholder := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mustExec(t, pool, `INSERT INTO core.pull_request_threads (id, pull_request_id, commit_sha, path, line) VALUES ($1, $2, $3, 'a.go', 3)`, th, pr, sha1)
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body) VALUES ($1, $1, $2, $3, 'riga')`, th, pr, user)
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body) VALUES ($1, $2, $3, $4, 'risposta')`, reply, th, pr, user)

	// Commento generale: thread_id NULL, pull_request_id obbligatorio.
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body) VALUES ($1, NULL, $2, $3, 'generale')`, general, pr, user)
	_, err := pool.Exec(ctx, `INSERT INTO core.pull_request_comments (id, thread_id, author_id, body) VALUES ($1, NULL, $2, 'x')`, uuid.New(), user)
	wantCode(t, err, "23502")
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_comments (id, pull_request_id, author_id, body) VALUES ($1, $2, $3, '')`, uuid.New(), pr, user)
	wantCode(t, err, "23514") // testo vuoto solo per un segnaposto
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_comments (id, pull_request_id, author_id, body, deleted) VALUES ($1, $2, $3, '', true)`, uuid.New(), pr, user)
	wantCode(t, err, "23514") // un segnaposto vive in una conversazione

	// Segnaposto: la radice di una seconda conversazione, con testo vuoto.
	th2 := placeholder
	mustExec(t, pool, `INSERT INTO core.pull_request_threads (id, pull_request_id, commit_sha, path, line) VALUES ($1, $2, $3, 'b.go', 1)`, th2, pr, sha1)
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body, deleted) VALUES ($1, $1, $2, $3, '', true)`, th2, pr, user)
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body) VALUES ($1, $2, $3, $4, 'resta')`, uuid.New(), th2, pr, user)

	// Giù con i commenti generali e il segnaposto presenti.
	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0021 con commenti generali: %v", err)
	}
	var total, generalLeft, hasCols int
	var body string
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE id = $1), (SELECT body FROM core.pull_request_comments WHERE id = $2)
		FROM core.pull_request_comments`, general, placeholder).Scan(&total, &generalLeft, &body); err != nil {
		t.Fatal(err)
	}
	if total != 4 || generalLeft != 0 || body != "[eliminato]" {
		t.Fatalf("dopo il down: commenti=%d generali=%d segnaposto=%q", total, generalLeft, body)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='core' AND table_name='pull_request_comments'
		AND column_name IN ('pull_request_id','deleted')`).Scan(&hasCols); err != nil || hasCols != 0 {
		t.Fatalf("colonne rimaste dopo il down: %d %v", hasCols, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_comments (id, thread_id, author_id, body) VALUES ($1, NULL, $2, 'x')`, uuid.New(), user)
	wantCode(t, err, "23502") // thread_id torna NOT NULL

	// Su ancora: pull_request_id si ricava dalla conversazione.
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0021: %v", err)
	}
	var withPR int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_comments WHERE pull_request_id = $1 AND NOT deleted`, pr).Scan(&withPR); err != nil || withPR != 4 {
		t.Fatalf("commenti con pull_request_id = %d (%v)", withPR, err)
	}
}
