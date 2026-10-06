//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// 0012 (GIT-135): webhooked_at sull'outbox. Gli eventi già presenti si
// segnano elaborati (non generano consegne), i nuovi nascono da elaborare; il
// down toglie colonna e indice e un nuovo up li ricrea.
func TestMigration0012_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0012: %v", err)
	}
	q := `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'event_outbox' AND column_name = 'webhooked_at')`
	idx := `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'event_outbox_unwebhooked')`
	var has, hasIdx bool
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || has {
		t.Fatalf("webhooked_at dopo il down: %v %v", has, err)
	}
	if err := pool.QueryRow(ctx, idx).Scan(&hasIdx); err != nil || hasIdx {
		t.Fatalf("indice dopo il down: %v %v", hasIdx, err)
	}
	// 0010 resta: notified_at c'è ancora.
	var notified bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'event_outbox' AND column_name = 'notified_at')`).Scan(&notified); err != nil || !notified {
		t.Fatalf("notified_at deve restare: %v %v", notified, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0012: %v", err)
	}
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || !has {
		t.Fatalf("webhooked_at dopo il up: %v %v", has, err)
	}
	if err := pool.QueryRow(ctx, idx).Scan(&hasIdx); err != nil || !hasIdx {
		t.Fatalf("indice dopo il up: %v %v", hasIdx, err)
	}
	var old, fresh int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NOT NULL`).Scan(&old); err != nil || old != 1 {
		t.Fatalf("eventi preesistenti segnati elaborati = %d (%v)", old, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NULL`).Scan(&fresh); err != nil || fresh != 1 {
		t.Fatalf("eventi nuovi da elaborare = %d (%v)", fresh, err)
	}
}
