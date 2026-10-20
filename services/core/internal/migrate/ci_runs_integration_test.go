//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0024 (GIT-276): esecuzioni, job, passi, orari e stato CI del repo. Il down
// toglie le tabelle senza toccare il resto; i vincoli reggono.
func TestMigration0024_EsecuzioniECoda(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "ci")
	owner := uuid.New()
	sha := "0123456789abcdef0123456789abcdef01234567"
	tables := []string{"ci_repo_state", "ci_runs", "ci_jobs", "ci_steps", "ci_schedules"}
	have := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
			WHERE table_schema = 'core' AND table_name = ANY($1)`, tables).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := have(); n != len(tables) {
		t.Fatalf("tabelle presenti %d, volute %d", n, len(tables))
	}

	run := uuid.New()
	insertRun := `INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, schedule_slot)
		VALUES ($1, $2, 'user', $3, '.github/workflows/ci.yml', $4, 'refs/heads/main', $5, 'push:refs/heads/main', $6)`
	mustExec(t, pool, insertRun, run, repo, owner, "push", sha, nil)
	// Default: in coda, senza conclusione.
	var status string
	var concl *string
	if err := pool.QueryRow(ctx, `SELECT status, conclusion FROM core.ci_runs WHERE id = $1`, run).Scan(&status, &concl); err != nil || status != "queued" || concl != nil {
		t.Fatalf("default: %s %v %v", status, concl, err)
	}
	// Vincoli: evento sconosciuto, sha non valido, schedule senza slot, slot su un push.
	for name, args := range map[string][]any{
		"evento":        {uuid.New(), repo, owner, "issues", sha, nil},
		"sha":           {uuid.New(), repo, owner, "push", "xyz", nil},
		"schedule/slot": {uuid.New(), repo, owner, "schedule", sha, nil},
		"slot/push":     {uuid.New(), repo, owner, "push", sha, "2026-10-19T10:00:00Z"},
	} {
		if _, err := pool.Exec(ctx, insertRun, args...); err == nil {
			t.Errorf("%s: la riga doveva essere rifiutata", name)
		}
	}
	// La stessa scadenza di uno schedule non si inserisce due volte.
	mustExec(t, pool, insertRun, uuid.New(), repo, owner, "schedule", sha, "2026-10-19T10:00:00Z")
	if _, err := pool.Exec(ctx, insertRun, uuid.New(), repo, owner, "schedule", sha, "2026-10-19T10:00:00Z"); err == nil {
		t.Error("la scadenza doppia doveva essere rifiutata")
	}
	// Un run completato vuole completed_at.
	if _, err := pool.Exec(ctx, `UPDATE core.ci_runs SET status = 'completed', conclusion = 'success' WHERE id = $1`, run); err == nil {
		t.Error("completed senza completed_at doveva essere rifiutato")
	}

	job := uuid.New()
	mustExec(t, pool, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name) VALUES ($1, $2, 0, 'build', 'build')`, job, run)
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name) VALUES ($1, $2, 1, 'build', 'b2')`, uuid.New(), run); err == nil {
		t.Error("job_key doppio nello stesso run")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, runner_type) VALUES ($1, $2, 1, 'x', 'x', 'vm')`, uuid.New(), run); err == nil {
		t.Error("runner_type sconosciuto")
	}
	mustExec(t, pool, `INSERT INTO core.ci_steps (id, job_id, number, name) VALUES ($1, $2, 1, 'checkout')`, uuid.New(), job)
	var perm string
	if err := pool.QueryRow(ctx, `SELECT permissions::text FROM core.ci_jobs WHERE id = $1`, job).Scan(&perm); err != nil || perm == "" {
		t.Fatalf("permessi di default: %q %v", perm, err)
	}
	// Eliminando il repo spariscono run, job e passi.
	if _, err := pool.Exec(ctx, `DELETE FROM core.repositories WHERE resource_id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.ci_runs) + (SELECT count(*) FROM core.ci_jobs) + (SELECT count(*) FROM core.ci_steps)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("righe rimaste dopo l'eliminazione del repo: %d (%v)", left, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 5); err != nil {
		t.Fatalf("down 0024: %v", err)
	}
	if n := have(); n != 0 {
		t.Fatalf("tabelle dopo il down: %d", n)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0024 dopo down: %v", err)
	}
	if n := have(); n != len(tables) {
		t.Fatalf("tabelle dopo il nuovo up: %d", n)
	}
}
