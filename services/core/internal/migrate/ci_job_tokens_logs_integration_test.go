//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigrate0027_TokenELogDelJob: task_id numerico per il protocollo runner.v1 (anche
// per i job già esistenti), token del job con solo l'hash, contatore dei log; il down
// toglie tutto e il nuovo up rifunziona.
func TestMigrate0027_TokenELogDelJob(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	repo, run, job1, job2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo)
	exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, uuid.New())
	exec(`INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key)
		VALUES ($1, $2, 'user', $3, 'w.yml', 'push', 'refs/heads/main', repeat('a', 40), 'g')`, run, repo, uuid.New())
	exec(`INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name) VALUES ($1, $2, 0, 'a', 'a'), ($3, $2, 1, 'b', 'b')`, job1, run, job2)

	var t1, t2 int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT task_id FROM core.ci_jobs WHERE id = $1), (SELECT task_id FROM core.ci_jobs WHERE id = $2)`, job1, job2).Scan(&t1, &t2); err != nil || t1 == t2 || t1 < 1 {
		t.Fatalf("task_id: %d %d %v", t1, t2, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE core.ci_jobs SET task_id = 99 WHERE id = $1`, job1); err == nil {
		t.Error("task_id è generato: non si deve poter scrivere")
	}
	exec(`INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, decode(repeat('ab', 32), 'hex'), now(), now())`, job1)
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, decode('abcd', 'hex'), now(), now())`, job2); err == nil {
		t.Error("un hash di 2 byte non è uno SHA-256")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, decode(repeat('ab', 32), 'hex'), now(), now())`, job2); err == nil {
		t.Error("due job non possono avere lo stesso hash")
	}
	exec(`INSERT INTO core.ci_job_logs (job_id, lines, bytes) VALUES ($1, 3, 100)`, job1)
	// Eliminando il job spariscono token e contatore dei log.
	exec(`DELETE FROM core.ci_jobs WHERE id = $1`, job1)
	var left int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.ci_job_tokens) + (SELECT count(*) FROM core.ci_job_logs)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("righe rimaste: %d %v", left, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 10); err != nil {
		t.Fatalf("down 0027: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'ci_jobs' AND column_name = 'task_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("task_id dopo il down: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('ci_job_tokens', 'ci_job_logs')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT task_id FROM core.ci_jobs WHERE id = $1`, job2).Scan(&t2); err != nil || t2 < 1 {
		t.Fatalf("task_id dopo il nuovo up: %d %v", t2, err)
	}
}
