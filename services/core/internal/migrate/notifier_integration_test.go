//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// 0010 (GIT-133): notified_at sull'outbox. Gli eventi già presenti si
// segnano letti (non generano notifiche), i nuovi nascono non letti; il down
// toglie colonna e indice.
func TestMigration0010_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0010: %v", err)
	}
	var has bool
	q := `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'event_outbox' AND column_name = 'notified_at')`
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || has {
		t.Fatalf("notified_at dopo il down: %v %v", has, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0010: %v", err)
	}
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || !has {
		t.Fatalf("notified_at dopo il up: %v %v", has, err)
	}
	var old, fresh int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE notified_at IS NOT NULL`).Scan(&old); err != nil || old != 1 {
		t.Fatalf("eventi preesistenti segnati letti = %d (%v)", old, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE notified_at IS NULL`).Scan(&fresh); err != nil || fresh != 1 {
		t.Fatalf("eventi nuovi non letti = %d (%v)", fresh, err)
	}
}
