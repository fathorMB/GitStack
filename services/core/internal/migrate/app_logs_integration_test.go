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

// TestMigration0041_AppLogs (GIT-350, H12): righe di log delle app. La sorgente è app o platform, il
// nome della copia è facoltativo, i byte sono la lunghezza del testo, la cascata dall'app le toglie
// e il down toglie la sola tabella.
func TestMigration0041_AppLogs(t *testing.T) {
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
	app := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, app, repo, now); err != nil {
		t.Fatal(err)
	}

	if err := exec(`INSERT INTO core.app_log_lines (app_id, source, copy, at, message) VALUES ($1, 'app', 'web-1', $2, 'avvio')`, app, now); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.app_log_lines (app_id, source, copy, at, message) VALUES ($1, 'platform', NULL, $2, 'deploy')`, app, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_log_lines (app_id, source, copy, at, message) VALUES ($1, 'altro', NULL, $2, 'x')`, app, now) == nil {
		t.Error("la sorgente deve essere app o platform")
	}
	if exec(`INSERT INTO core.app_log_lines (app_id, source, copy, at, message) VALUES ($1, 'app', '', $2, 'x')`, app, now) == nil {
		t.Error("il nome della copia, se c'è, non è vuoto")
	}
	var bytes int
	if err := pool.QueryRow(ctx, `SELECT bytes FROM core.app_log_lines WHERE message = 'avvio'`).Scan(&bytes); err != nil || bytes != len("avvio") {
		t.Fatalf("byte della riga: %d %v (voluti %d)", bytes, err, len("avvio"))
	}

	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_log_lines`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dopo l'eliminazione dell'app restano %d righe di log: %v", n, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 14); err != nil {
		t.Fatalf("down 0041: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_log_lines'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella dei log deve sparire con il down 0041: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'apps'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("la tabella apps deve restare: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_log_lines`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella dei log dopo l'up: %d %v", n, err)
	}
}
