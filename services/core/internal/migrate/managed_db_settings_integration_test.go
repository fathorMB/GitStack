//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0059_ManagedDBSettings (GIT-557, ADR 0003, DB23): una sola riga di impostazioni d'istanza, major
// solo 16 e 17, valori positivi, intervallo delle porte coerente; il down toglie la tabella e lascia i database.
func TestMigration0059_ManagedDBSettings(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	if err := exec(`INSERT INTO core.managed_db_settings (singleton) VALUES (true)`); err != nil {
		t.Fatalf("riga vuota (tutto nil = valori del chart): %v", err)
	}
	if err := exec(`INSERT INTO core.managed_db_settings (singleton) VALUES (false)`); err == nil {
		t.Error("singleton false doveva essere rifiutato")
	}
	if err := exec(`INSERT INTO core.managed_db_settings (singleton) VALUES (true)`); err == nil {
		t.Error("una seconda riga doveva essere rifiutata")
	}
	for _, c := range []struct{ name, set string }{
		{"major 15", `allowed_majors = ARRAY[15]`},
		{"nessuna major", `allowed_majors = ARRAY[]::integer[]`},
		{"dimensione a zero", `max_size_bytes = 0`},
		{"cpu a zero", `max_cpu_millis = 0`},
		{"memoria a zero", `max_memory_bytes = 0`},
		{"porta sotto 1024", `lan_port_from = 80, lan_port_to = 90`},
		{"intervallo capovolto", `lan_port_from = 20010, lan_port_to = 20000`},
		{"un solo estremo", `lan_port_from = 20000`},
		{"dump programmati a zero", `keep_scheduled = 0`},
		{"dump su richiesta a zero", `keep_on_demand = 0`},
	} {
		if err := exec(`UPDATE core.managed_db_settings SET ` + c.set); err == nil {
			t.Errorf("%s: doveva essere rifiutato", c.name)
		}
	}
	if err := exec(`UPDATE core.managed_db_settings SET allowed_majors = ARRAY[16,17], max_size_bytes = 1, max_cpu_millis = 1, max_memory_bytes = 1,
		lan_port_from = 20000, lan_port_to = 20010, keep_scheduled = 1, keep_on_demand = 1`); err != nil {
		t.Fatalf("valori validi: %v", err)
	}
	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0059: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'managed_db_settings'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("managed_db_settings dovrebbe sparire con il down: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'managed_dbs'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("managed_dbs deve restare: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0059 dopo il down: %v", err)
	}
}
