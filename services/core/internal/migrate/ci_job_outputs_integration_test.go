//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0055 (GIT-501): outputs del job. La colonna è un JSON vuoto di default, accetta gli outputs
// e il down la toglie senza toccare i job.
func TestMigration0055_OutputsDelJob(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "outputs")
	owner := uuid.New()
	sha := "0123456789abcdef0123456789abcdef01234567"
	run, job := uuid.New(), uuid.New()
	mustExec(t, pool, `INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key)
		VALUES ($1, $2, 'user', $3, '.github/workflows/ci.yml', 'push', 'refs/heads/main', $4, 'push:refs/heads/main')`,
		run, repo, owner, sha)
	mustExec(t, pool, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status) VALUES ($1, $2, 0, 'build', 'build', 'in_progress')`, job, run)

	var out string
	if err := pool.QueryRow(ctx, `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, job).Scan(&out); err != nil || out != "{}" {
		t.Fatalf("default: %q %v", out, err)
	}
	mustExec(t, pool, `UPDATE core.ci_jobs SET outputs = '{"digest":"sha256:abc"}' WHERE id = $1`, job)

	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0055: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'ci_jobs' AND column_name = 'outputs'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("colonna dopo il down: %d (%v)", n, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM core.ci_jobs WHERE id = $1`, job).Scan(&status); err != nil || status != "in_progress" {
		t.Fatalf("il job dopo il down: %s (%v)", status, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0055 dopo down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, job).Scan(&out); err != nil || out != "{}" {
		t.Fatalf("dopo il nuovo up gli outputs ripartono vuoti: %q %v", out, err)
	}
}
