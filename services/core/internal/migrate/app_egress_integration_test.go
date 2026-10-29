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

// TestMigration0049_AppEgress (GIT-385, H16): destinazioni private aperte dall'amministratore (host e
// porta unici) e restrizione per app (un array JSON, una riga per app, via con l'app); il down toglie
// le due tabelle.
func TestMigration0049_AppEgress(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	now := time.Now().UTC()
	ins := `INSERT INTO core.app_private_destinations (id, host, port, created_at) VALUES ($1, $2, $3, $4)`
	if err := exec(ins, uuid.New(), "db.azienda.lan", 5432, now); err != nil {
		t.Fatal(err)
	}
	if exec(ins, uuid.New(), "db.azienda.lan", 5432, now) == nil {
		t.Error("la stessa destinazione non entra due volte")
	}
	if err := exec(ins, uuid.New(), "db.azienda.lan", 5433, now); err != nil {
		t.Errorf("stesso host, altra porta: %v", err)
	}
	for _, port := range []int{0, 65536} {
		if exec(ins, uuid.New(), "x.lan", port, now) == nil {
			t.Errorf("la porta %d non è valida", port)
		}
	}
	if exec(ins, uuid.New(), "", 80, now) == nil {
		t.Error("l'host non può essere vuoto")
	}

	repo, owner := uuid.New(), uuid.New()
	if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/m')`, repo); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'm')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	app := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, app, repo, now); err != nil {
		t.Fatal(err)
	}
	restr := `INSERT INTO core.app_egress_restrictions (app_id, destinations, updated_at) VALUES ($1, $2::jsonb, $3)`
	if err := exec(restr, app, `[{"host":"db.azienda.lan","port":5432}]`, now); err != nil {
		t.Fatal(err)
	}
	if exec(restr, app, `[]`, now) == nil {
		t.Error("una restrizione per app")
	}
	if exec(`INSERT INTO core.app_egress_restrictions (app_id, destinations, updated_at) VALUES ($1, '{}'::jsonb, $2)`, uuid.New(), now) == nil {
		t.Error("l'app deve esistere")
	}
	other := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'api', 100, 1000, 200, 2000, $3, $3)`, other, repo, now); err != nil {
		t.Fatal(err)
	}
	if exec(restr, other, `{"host":"x"}`, now) == nil {
		t.Error("destinations deve essere un array")
	}
	if err := exec(restr, other, `[]`, now); err != nil {
		t.Errorf("un elenco vuoto è una restrizione valida: %v", err)
	}
	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.app_egress_restrictions WHERE app_id = $1`, app).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la restrizione esce con l'app: %d %v", n, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0049: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('app_private_destinations', 'app_egress_restrictions')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("le tabelle devono sparire con il down 0049: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
