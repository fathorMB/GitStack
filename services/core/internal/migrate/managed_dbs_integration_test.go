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

// TestMigration0057_ManagedDBs (GIT-551, ADR 0003): vincoli di core.managed_dbs (major 16/17, nome DNS unico
// per owner, uno stato tra quelli del contratto, porta LAN unica), link a cascata e down senza residui.
func TestMigration0057_ManagedDBs(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(owner, name string, major int, status string) (uuid.UUID, error) {
		id := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO core.managed_dbs (id, owner_name, owner_type, owner_id, name, pg_major, size_bytes, cpu_millis, memory_bytes, status, created_at, updated_at)
			VALUES ($1,$2,'user',$3,$4,$5,1073741824,100,268435456,$6,$7,$7)`, id, owner, uuid.New(), name, major, status, now)
		return id, err
	}
	id, err := insert("alice", "shop", 17, "creating")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what                string
		owner, name, status string
		major               int
	}{
		{"nome duplicato per lo stesso owner", "alice", "shop", "creating", 17},
		{"major non ammessa", "alice", "old", "creating", 15},
		{"stato sconosciuto", "alice", "x", "boh", 17},
		{"owner non minuscolo", "Alice", "x", "creating", 17},
		{"nome non DNS", "alice", "Con_Maiuscole", "creating", 17},
	} {
		if _, err := insert(c.owner, c.name, c.major, c.status); err == nil {
			t.Errorf("%s: doveva essere rifiutato", c.what)
		}
	}
	if _, err := insert("bob", "shop", 16, "running"); err != nil {
		t.Errorf("lo stesso nome per un altro owner e' libero: %v", err)
	}
	var lan, disk string
	if err := pool.QueryRow(ctx, `SELECT lan_enabled::text, disk_state FROM core.managed_dbs WHERE id = $1`, id).Scan(&lan, &disk); err != nil || lan != "false" || disk != "none" {
		t.Fatalf("default: lan=%s disk=%s %v", lan, disk, err)
	}
	// Porta LAN unica.
	if _, err := pool.Exec(ctx, `UPDATE core.managed_dbs SET lan_enabled = true, lan_port = 15432 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	other, _ := insert("carol", "x", 17, "creating")
	if _, err := pool.Exec(ctx, `UPDATE core.managed_dbs SET lan_port = 15432 WHERE id = $1`, other); err == nil {
		t.Error("la porta LAN deve essere unica")
	}
	// Audit senza chiave esterna: sopravvive all'eliminazione del database.
	if _, err := pool.Exec(ctx, `INSERT INTO core.managed_db_audit (db_id, owner_name, name, action, at) VALUES ($1,'alice','shop','created',$2)`, id, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM core.managed_dbs WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.managed_db_audit WHERE db_id = $1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("l'audit sopravvive all'eliminazione: %d %v", n, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 3); err != nil {
		t.Fatalf("down 0057: %v", err)
	}
	for _, table := range []string{"managed_dbs", "managed_db_links", "managed_db_audit"} {
		var c int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = $1`, table).Scan(&c); err != nil || c != 0 {
			t.Errorf("%s dovrebbe sparire con il down: %d %v", table, c, err)
		}
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0057 dopo il down: %v", err)
	}
}
