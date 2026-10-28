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

// TestMigration0043_AppMetrics (GIT-384, H12): campioni delle metriche per copia. Riavvii e pronto
// sono obbligatori; CPU e memoria possono mancare (sorgente non risposta) ma non essere negative; il
// nome della copia non è vuoto; la cascata dall'app li toglie; il down toglie la tabella.
func TestMigration0043_AppMetrics(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	repo, owner := uuid.New(), uuid.New()
	if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/m')`, repo); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'm')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	app := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, app, repo, now); err != nil {
		t.Fatal(err)
	}

	if err := exec(`INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts, cpu_millicores, memory_bytes)
		VALUES ($1, 'web-1', $2, true, 0, 12, 52428800)`, app, now); err != nil {
		t.Fatalf("campione completo: %v", err)
	}
	if err := exec(`INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts)
		VALUES ($1, 'web-1', $2, false, 3)`, app, now); err != nil {
		t.Fatalf("campione senza sorgente (CPU e memoria NULL): %v", err)
	}
	for name, q := range map[string]string{
		"riavvii negativi": `INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts) VALUES ($1, 'w', $2, true, -1)`,
		"copia vuota":      `INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts) VALUES ($1, '', $2, true, 0)`,
		"CPU negativa":     `INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts, cpu_millicores) VALUES ($1, 'w', $2, true, 0, -5)`,
		"memoria negativa": `INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts, memory_bytes) VALUES ($1, 'w', $2, true, 0, -5)`,
		"pronto mancante":  `INSERT INTO core.app_metric_samples (app_id, copy, at, restarts) VALUES ($1, 'w', $2, 0)`,
	} {
		if exec(q, app, now) == nil {
			t.Errorf("il vincolo deve rifiutare: %s", name)
		}
	}

	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_metric_samples`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("i campioni devono uscire con l'app: %d %v", n, err)
	}

	// Il down della 0043 e della 0044 (sopra di essa) toglie la tabella.
	if err := migrate.Down(ctx, pool, dsn, 10); err != nil {
		t.Fatalf("down 0043: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_metric_samples`).Scan(&n); err == nil {
		t.Fatal("la tabella dei campioni deve sparire con il down 0043")
	}
}
