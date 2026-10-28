//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0052_AppDomainCustomCert (GIT-460): le colonne del certificato del cliente;// il down le toglie.
func TestMigration0052_AppDomainCustomCert(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	var n int
	for _, col := range []string{"cert_pem", "cert_key_ciphertext", "cert_key_nonce", "cert_key_id"} {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_domains' AND column_name = $1`, col).Scan(&n); err != nil || n != 1 {
			t.Fatalf("colonna %s: %d %v", col, n, err)
		}
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0052: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_domains' AND column_name IN ('cert_pem','cert_key_ciphertext','cert_key_nonce','cert_key_id')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("le colonne devono sparire con il down 0052: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
