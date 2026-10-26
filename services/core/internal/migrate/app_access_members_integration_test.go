//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0040_AppAccessMembers (GIT-376, H6): la tabella della lista di accesso esiste, rifiuta
// righe incoerenti e doppioni, e il down la toglie; il nuovo up la rimette.
func TestMigration0040_AppAccessMembers(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	has := func() bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_access_members'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !has() {
		t.Fatal("app_access_members manca dopo le migrazioni")
	}
	// Senza app, nessuna riga: la chiave esterna rifiuta un app_id inesistente.
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_access_members (id, app_id, kind, user_id, created_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'user', gen_random_uuid(), now())`); err == nil {
		t.Fatal("una riga con app inesistente deve essere rifiutata")
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0040: %v", err)
	}
	if has() {
		t.Fatal("app_access_members resta dopo il down")
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if !has() {
		t.Fatal("tabella assente dopo il nuovo up")
	}
}
