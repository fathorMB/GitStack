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

// TestMigration0048_AppTraffic (GIT-403, H12): campioni di richieste ed errori 5xx per app. Le
// differenze non sono negative e gli errori non superano le richieste; la cascata dall'app li toglie;
// il down toglie la tabella.
func TestMigration0048_AppTraffic(t *testing.T) {
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
	if err := exec(`INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, 10, 2)`, app, now); err != nil {
		t.Fatalf("campione valido: %v", err)
	}
	if err := exec(`INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, 0, 0)`, app, now); err != nil {
		t.Fatalf("campione a zero: %v", err)
	}
	for name, q := range map[string]string{
		"richieste negative":       `INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, -1, 0)`,
		"errori negativi":          `INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, 5, -1)`,
		"più errori che richieste": `INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, 1, 2)`,
		"app mancante":             `INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx) VALUES ($1, $2, 1, 0)`,
	} {
		args := []any{app, now}
		if name == "app mancante" {
			args = []any{uuid.New(), now}
		}
		if exec(q, args...) == nil {
			t.Errorf("il vincolo deve rifiutare: %s", name)
		}
	}
	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_traffic_samples`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("i campioni devono uscire con l'app: %d %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 4); err != nil {
		t.Fatalf("down 0048: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_traffic_samples`).Scan(&n); err == nil {
		t.Fatal("la tabella dei campioni deve sparire con il down 0048")
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
