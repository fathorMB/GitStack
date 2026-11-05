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

// TestMigration0058_ManagedDBBackups (GIT-553, ADR 0003, DB6, DB13, DB15): tipi e stati dei dump, un solo dump
// `in_progress` per database, dump a cascata con il database, colonne di backup/ripristino/aggiornamento
// e down che toglie solo quello che ha aggiunto (il database gestito resta).
func TestMigration0058_ManagedDBBackups(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_dbs (id, owner_name, owner_type, owner_id, name, pg_major, size_bytes, cpu_millis, memory_bytes, status, created_at, updated_at)
		VALUES ($1,'alice','user',$2,'shop',17,1073741824,100,268435456,'running',$3,$3)`, id, uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, status string) error {
		_, err := pool.Exec(ctx, `INSERT INTO core.managed_db_backups (id, db_id, kind, status, created_at) VALUES ($1,$2,$3,$4,$5)`, uuid.New(), id, kind, status, now)
		return err
	}
	for _, k := range []string{"scheduled", "on_demand", "safety"} {
		if err := insert(k, "completed"); err != nil {
			t.Errorf("tipo %s: %v", k, err)
		}
	}
	if err := insert("manuale", "completed"); err == nil {
		t.Error("un tipo sconosciuto doveva essere rifiutato")
	}
	if err := insert("scheduled", "boh"); err == nil {
		t.Error("uno stato sconosciuto doveva essere rifiutato")
	}
	if err := insert("on_demand", "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := insert("scheduled", "in_progress"); err == nil {
		t.Error("due dump in_progress per lo stesso database: doveva essere rifiutato (backup_in_progress)")
	}
	if _, err := pool.Exec(ctx, `UPDATE core.managed_dbs SET restore_mode = 'dappertutto' WHERE id = $1`, id); err == nil {
		t.Error("un modo di ripristino sconosciuto doveva essere rifiutato")
	}
	if _, err := pool.Exec(ctx, `UPDATE core.managed_dbs SET backup_error = 'x', backup_error_kind = 'scheduled', upgrade_requested_at = $2, restore_mode = 'same' WHERE id = $1`, id, now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.managed_db_backups WHERE db_id = $1`, id).Scan(&n); err != nil || n != 4 {
		t.Fatalf("dump scritti: %d %v", n, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0058: %v", err)
	}
	var c int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'managed_db_backups'`).Scan(&c); err != nil || c != 0 {
		t.Errorf("managed_db_backups dovrebbe sparire con il down: %d %v", c, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'managed_dbs' AND column_name IN ('backup_error','upgrade_requested_at','restore_mode','restore_file')`).Scan(&c); err != nil || c != 0 {
		t.Errorf("le colonne aggiunte dovrebbero sparire: %d %v", c, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.managed_dbs WHERE id = $1`, id).Scan(&c); err != nil || c != 1 {
		t.Errorf("il database gestito deve restare: %d %v", c, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0058 dopo il down: %v", err)
	}
	// I dump seguono il database (ON DELETE CASCADE).
	if err := insert("on_demand", "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM core.managed_dbs WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.managed_db_backups WHERE db_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dump rimasti dopo l'eliminazione del database: %d %v", n, err)
	}
}
