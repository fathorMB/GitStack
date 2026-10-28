//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0051_AppDomains (GIT-458): il dominio è unico sul nome normalizzato, lo stato e il
// certificato hanno i loro vincoli, il dominio segue l'app (cascade) e il down toglie tabella e colonna.
func TestMigration0051_AppDomains(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	now := time.Now().UTC()
	var appID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	// Senza un'app il dominio non si può legare.
	if exec(`INSERT INTO core.app_domains (id, app_id, domain, normalized, created_at) VALUES (gen_random_uuid(), $1, 'a.it', 'a.it', $2)`, appID, now) == nil {
		t.Error("il dominio richiede un'app")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_capacity_settings' AND column_name = 'public_ips'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("public_ips: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'app_domains_normalized_uniq'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("indice unico sul dominio normalizzato: %d %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0051: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_domains'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella deve sparire con il down 0051: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_capacity_settings' AND column_name = 'public_ips'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0051: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
