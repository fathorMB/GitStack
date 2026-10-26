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

// TestMigration0037_AppEnv (GIT-348, H8): variabili e segreti delle app in tabelle proprie; nome unico
// per app, nonce di 12 byte, cascata dall'app; il down toglie le due tabelle e il nuovo up rifunziona.
func TestMigration0037_AppEnv(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	repo, owner := uuid.New(), uuid.New()
	if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	app := uuid.New()
	if err := exec(`INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000, 200, 2000, $3, $3)`, app, repo, now); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)

	// Variabili: una per nome e app.
	if err := exec(`INSERT INTO core.app_variables (app_id, name, value, version, updated_at) VALUES ($1, 'REGION', 'eu', 2, $2)`, app, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_variables (app_id, name, value, version, updated_at) VALUES ($1, 'REGION', 'us', 3, $2)`, app, now) == nil {
		t.Error("il nome della variabile è unico per app")
	}
	if exec(`INSERT INTO core.app_variables (app_id, name, value, version, updated_at) VALUES ($1, '1X', 'x', 2, $2)`, app, now) == nil {
		t.Error("un nome di variabile non può iniziare per cifra")
	}

	// Segreti: cifrati, nonce di 12 byte, nome unico per app.
	sid := uuid.New()
	if err := exec(`INSERT INTO core.app_secrets (id, app_id, name, ciphertext, nonce, secret_key_id, version, updated_at)
		VALUES ($1, $2, 'TOKEN', '\x01', $3, 'k1', 2, $4)`, sid, app, nonce, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_secrets (id, app_id, name, ciphertext, nonce, secret_key_id, version, updated_at)
		VALUES ($1, $2, 'TOKEN', '\x01', $3, 'k1', 3, $4)`, uuid.New(), app, nonce, now) == nil {
		t.Error("il nome del segreto è unico per app")
	}
	if exec(`INSERT INTO core.app_secrets (id, app_id, name, ciphertext, nonce, secret_key_id, version, updated_at)
		VALUES ($1, $2, 'SHORT', '\x01', '\x01', 'k1', 2, $3)`, uuid.New(), app, now) == nil {
		t.Error("il nonce deve essere di 12 byte")
	}

	// Nessun segreto in una tabella di variabili, e viceversa: la separazione è nelle tabelle.
	if count("app_variables") != 1 || count("app_secrets") != 1 {
		t.Fatalf("righe: %d variabili, %d segreti", count("app_variables"), count("app_secrets"))
	}

	// Cascata dall'app: spariscono variabili e segreti.
	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app); err != nil {
		t.Fatal(err)
	}
	if count("app_variables") != 0 || count("app_secrets") != 0 {
		t.Fatalf("dopo l'eliminazione dell'app restano righe: %d variabili, %d segreti", count("app_variables"), count("app_secrets"))
	}

	if err := migrate.Down(ctx, pool, dsn, 7); err != nil {
		t.Fatalf("down 0038+0037: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
		AND table_name IN ('app_variables', 'app_secrets')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	// Le tabelle delle app restano: il down è solo della 0037.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'apps'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("la tabella apps deve restare dopo il down 0037: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if count("app_variables") != 0 || count("app_secrets") != 0 {
		t.Error("tabelle non vuote dopo il nuovo up")
	}
}
