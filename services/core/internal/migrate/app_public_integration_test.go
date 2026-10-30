//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0050_AppPublic (GIT-456, H1): l'interruttore delle app pubbliche è spento di default,
// lo storico dei cambi di accesso lega l'evento d'app a un'app (e quello d'istanza a nessuna), e il
// down toglie colonna e tabella.
func TestMigration0050_AppPublic(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO core.app_capacity_settings (singleton) VALUES (true)`); err != nil {
		t.Fatal(err)
	}
	var on bool
	if err := pool.QueryRow(ctx, `SELECT public_apps_enabled FROM core.app_capacity_settings`).Scan(&on); err != nil || on {
		t.Fatalf("di default le app pubbliche sono spente: %v %v", on, err)
	}
	if exec(`UPDATE core.app_capacity_settings SET public_apps_enabled = NULL`) == nil {
		t.Error("public_apps_enabled non può essere NULL")
	}
	now := time.Now().UTC()
	if err := exec(`INSERT INTO core.app_access_events (kind, new_value, created_at) VALUES ('public_apps_enabled', 'true', $1)`, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_access_events (kind, new_value, created_at) VALUES ('app_access', 'public', $1)`, now) == nil {
		t.Error("un evento d'app richiede l'app")
	}
	if exec(`INSERT INTO core.app_access_events (kind, new_value, created_at) VALUES ('altro', 'x', $1)`, now) == nil {
		t.Error("tipo di evento sconosciuto")
	}
	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0050: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_capacity_settings' AND column_name = 'public_apps_enabled'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0050: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_access_events'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella deve sparire con il down 0050: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
