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

// TestMigration0060_DBTelemetry (GIT-568, ADR 0003 DB21): i campioni di CPU/RAM e le righe di log
// dei database si conservano con la stessa semantica dei log delle app: campione senza CPU = la
// sorgente non ha risposto (mai zero), vincoli non negativi, a cascata sul database (con
// l'eliminazione sparisce la telemetria) e down senza residui.
func TestMigration0060_DBTelemetry(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	db := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_dbs (id, owner_name, owner_type, owner_id, name, pg_major, size_bytes, cpu_millis, memory_bytes, status, created_at, updated_at)
		VALUES ($1,'alice','user',$2,'shop',17,1073741824,100,268435456,'running',$3,$3)`, db, uuid.New(), now); err != nil {
		t.Fatal(err)
	}

	// Campione completo e campione «sorgente non risponde» (senza CPU né memoria): entrambi validi.
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_db_metric_samples (db_id, at, ready, restarts, cpu_millicores, memory_bytes)
		VALUES ($1,$2,true,1,42,1048576)`, db, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_db_metric_samples (db_id, at, ready, restarts, cpu_millicores, memory_bytes)
		VALUES ($1,$2,false,0,NULL,NULL)`, db, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Vincoli: CPU e riavvii non negativi.
	for _, sql := range []string{
		`INSERT INTO core.managed_db_metric_samples (db_id, at, ready, restarts, cpu_millicores, memory_bytes) VALUES ($1,$2,true,0,-1,1)`,
		`INSERT INTO core.managed_db_metric_samples (db_id, at, ready, restarts, cpu_millicores, memory_bytes) VALUES ($1,$2,true,-1,1,1)`,
	} {
		if _, err := pool.Exec(ctx, sql, db, now); err == nil {
			t.Errorf("doveva essere rifiutato: %s", sql)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_db_log_lines (db_id, at, message, bytes) VALUES ($1,$2,'database system is ready',25)`, db, now); err != nil {
		t.Fatal(err)
	}

	// Il webhook database e' selezionabile e consegnabile (DB20), come gli altri eventi.
	if _, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, org_id, url, events, active, created_by, created_at, updated_at)
		VALUES ($1,'org',$2,'http://hooks.example.test/in',ARRAY['database']::TEXT[],true,$3,$4,$4)`, uuid.New(), uuid.New(), uuid.New(), now); err != nil {
		t.Fatalf("webhook con evento database: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, org_id, url, events, active, created_by, created_at, updated_at)
		VALUES ($1,'org',$2,'http://hooks.example.test/in',ARRAY['databases']::TEXT[],true,$3,$4,$4)`, uuid.New(), uuid.New(), uuid.New(), now); err == nil {
		t.Error("l'evento 'databases' non esiste: doveva essere rifiutato")
	}

	// L'eliminazione del database porta via anche la telemetria (a cascata, come i link).
	if _, err := pool.Exec(ctx, `DELETE FROM core.managed_dbs WHERE id = $1`, db); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"managed_db_metric_samples": 0, "managed_db_log_lines": 0} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s dopo l'eliminazione: %d %v", table, n, err)
		}
	}

	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0060: %v", err)
	}
	for _, table := range []string{"managed_db_log_lines", "managed_db_metric_samples"} {
		var c int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = $1`, table).Scan(&c); err != nil || c != 0 {
			t.Errorf("%s dovrebbe sparire con il down: %d %v", table, c, err)
		}
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0060 dopo il down: %v", err)
	}
}
