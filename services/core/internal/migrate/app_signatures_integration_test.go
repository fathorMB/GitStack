//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigration0046_AppSignatures (GIT-349, H10): «solo immagini firmate» è spento di default, le
// chiavi fidate hanno impronta unica, e il down toglie colonna e tabella.
func TestMigration0046_AppSignatures(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO core.app_capacity_settings (singleton) VALUES (true)`); err != nil {
		t.Fatal(err)
	}
	var only bool
	if err := pool.QueryRow(ctx, `SELECT signed_images_only FROM core.app_capacity_settings`).Scan(&only); err != nil || only {
		t.Fatalf("di default le immagini non firmate sono accettate: %v %v", only, err)
	}
	if exec(`UPDATE core.app_capacity_settings SET signed_images_only = NULL`) == nil {
		t.Error("signed_images_only non può essere NULL")
	}
	fp := "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	now := time.Now().UTC()
	if err := exec(`INSERT INTO core.app_trusted_keys (id, name, fingerprint, public_key, created_at) VALUES ($1, 'ci esterna', $2, 'PEM', $3)`, uuid.New(), fp, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_trusted_keys (id, name, fingerprint, public_key, created_at) VALUES ($1, 'doppia', $2, 'PEM', $3)`, uuid.New(), fp, now) == nil {
		t.Error("la stessa impronta non entra due volte")
	}
	if exec(`INSERT INTO core.app_trusted_keys (id, name, fingerprint, public_key, created_at) VALUES ($1, 'x', 'non-esadecimale', 'PEM', $2)`, uuid.New(), now) == nil {
		t.Error("l'impronta deve essere SHA-256 in esadecimale")
	}
	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0046: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_capacity_settings' AND column_name = 'signed_images_only'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la colonna deve sparire con il down 0046: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'app_trusted_keys'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("la tabella deve sparire con il down 0046: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
