//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0053_AppDomainExpiryNotice (GIT-461): la colonna dello stato «già avvisato»; il down la toglie.
func TestMigration0053_AppDomainExpiryNotice(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	const q = `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_domains' AND column_name = 'expiry_notified_until'`
	var n int
	if err := pool.QueryRow(ctx, q).Scan(&n); err != nil || n != 1 {
		t.Fatalf("colonna expiry_notified_until: %d %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0053: %v", err)
	}
	if err := pool.QueryRow(ctx, q).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0053: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
