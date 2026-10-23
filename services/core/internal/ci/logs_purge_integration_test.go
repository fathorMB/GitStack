//go:build integration

package ci_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// purgeNow è l'orologio finto dei test di pulizia.
var purgeNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// seedPurgeRepo crea un repo dove stanno le esecuzioni di prova.
func seedPurgeRepo(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, "owner/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, created_at) VALUES ($1, 'user', $2, 'app', $3)`,
		id, owner, purgeNow.AddDate(-1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	return id
}

// seedLoggedJob crea un'esecuzione con un solo job nello stato dato (completed se
// finishedAt non è nil) e scrive nel suo log due righe con LogStore.Append.
func seedLoggedJob(t *testing.T, pool *pgxpool.Pool, logs *ci.LogStore, repo uuid.UUID, status string, finishedAt *time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	runID, jobID := uuid.New(), uuid.New()
	runStatus, runConclusion := "in_progress", any(nil)
	var completed any
	if finishedAt != nil {
		runStatus, runConclusion, completed = "completed", "success", *finishedAt
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, status, conclusion, created_at, completed_at)
		VALUES ($1, $2, 'user', $3, '.gitstack/workflows/ci.yml', 'push', 'refs/heads/main', $4, '', $5, $6, $7, $8)`,
		runID, repo, uuid.New(), strings.Repeat("a", 40), runStatus, runConclusion, purgeNow.AddDate(0, -6, 0), completed); err != nil {
		t.Fatal(err)
	}
	var jobCompleted, conclusion any
	if finishedAt != nil {
		jobCompleted = *finishedAt
		conclusion = "success"
		if status == "cancelled" {
			conclusion = "cancelled"
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status, conclusion, completed_at)
		VALUES ($1, $2, 0, 'build', 'build', $3, $4, $5)`, jobID, runID, status, conclusion, jobCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Append(ctx, pool, purgeNow, jobID, 0, []ci.LogLine{{Time: purgeNow, Content: "uno"}, {Time: purgeNow, Content: "due"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	return jobID
}

func purgeLogFile(dir string, jobID uuid.UUID) string {
	return filepath.Join(dir, jobID.String()+".log")
}

func logRows(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM core.ci_job_logs WHERE job_id = $1`, jobID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatal(err)
	return false
}

// TestPurgeOldLogs: con l'orologio finto, un log di un job finito da più di 90 giorni
// (completed o cancelled) viene eliminato, file e riga; uno di 89 giorni e uno di un
// job in corso restano. La pulizia è idempotente e non fallisce se il file manca.
func TestPurgeOldLogs(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	dir := t.TempDir()
	logs := &ci.LogStore{Dir: dir}
	repo := seedPurgeRepo(t, pool)
	ago := func(d time.Duration) *time.Time { x := purgeNow.Add(-d); return &x }

	oldDone := seedLoggedJob(t, pool, logs, repo, "completed", ago(91*24*time.Hour))
	oldCancelled := seedLoggedJob(t, pool, logs, repo, "cancelled", ago(400*24*time.Hour))
	recent := seedLoggedJob(t, pool, logs, repo, "completed", ago(89*24*time.Hour))
	running := seedLoggedJob(t, pool, logs, repo, "in_progress", nil)

	n, err := logs.PurgeOldLogs(ctx, pool, purgeNow, 0)
	if err != nil {
		t.Fatalf("pulizia: %v", err)
	}
	if n != 2 {
		t.Fatalf("job puliti = %d, voluto 2", n)
	}
	for _, id := range []uuid.UUID{oldDone, oldCancelled} {
		if fileExists(t, purgeLogFile(dir, id)) {
			t.Errorf("job %s: il file del log resta dopo i 90 giorni", id)
		}
		if r := logRows(t, pool, id); r != 0 {
			t.Errorf("job %s: la riga di core.ci_job_logs resta (%d)", id, r)
		}
	}
	for _, id := range []uuid.UUID{recent, running} {
		if !fileExists(t, purgeLogFile(dir, id)) {
			t.Errorf("job %s: il file del log è stato eliminato, non doveva", id)
		}
		if r := logRows(t, pool, id); r != 1 {
			t.Errorf("job %s: la riga di core.ci_job_logs è %d, voluto 1", id, r)
		}
	}

	// Idempotente: una seconda passata non trova nulla e non fallisce.
	if n, err := logs.PurgeOldLogs(ctx, pool, purgeNow, 0); err != nil || n != 0 {
		t.Fatalf("seconda pulizia: n=%d err=%v, voluto 0 e nil", n, err)
	}

	// Il file manca ma la riga resta (per esempio dopo un crash): la pulizia va avanti lo stesso.
	gone := seedLoggedJob(t, pool, logs, repo, "completed", ago(200*24*time.Hour))
	if err := os.Remove(purgeLogFile(dir, gone)); err != nil {
		t.Fatal(err)
	}
	if n, err := logs.PurgeOldLogs(ctx, pool, purgeNow, 0); err != nil || n != 1 {
		t.Fatalf("pulizia con file mancante: n=%d err=%v, voluto 1 e nil", n, err)
	}
	if r := logRows(t, pool, gone); r != 0 {
		t.Errorf("job con file mancante: la riga resta (%d)", r)
	}
}
