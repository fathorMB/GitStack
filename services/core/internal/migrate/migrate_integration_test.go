//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestUp_Idempotent verifica che applicare le migrazioni due volte non
// causi errori (idempotenza, criterio di accettazione): dbtest.NewPool ha
// già applicato Up una volta, qui lo rifacciamo esplicitamente.
func TestUp_Idempotent(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("una seconda chiamata a Up non deve fallire: %v", err)
	}

	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'resources'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("verifica tabella non riuscita: %v", err)
	}
	if !exists {
		t.Fatal("core.resources dovrebbe esistere dopo le migrazioni")
	}
}

// TestDown_RollbackDocumentato verifica che il rollback (0001.down.sql)
// rimuova la tabella creata dalla migrazione, e che Up la ricrei.
func TestDown_RollbackDocumentato(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Down(ctx, pool, dsn, 10); err != nil {
		t.Fatalf("rollback non riuscito: %v", err)
	}

	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'resources'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("verifica tabella non riuscita: %v", err)
	}
	if exists {
		t.Fatal("core.resources non dovrebbe più esistere dopo il rollback")
	}

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("riapplicare Up dopo Down non deve fallire: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'resources'
	)`).Scan(&exists); err != nil {
		t.Fatalf("verifica tabella non riuscita: %v", err)
	}
	if !exists {
		t.Fatal("core.resources dovrebbe esistere di nuovo dopo Up")
	}
}
