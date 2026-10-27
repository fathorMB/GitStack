//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0033_RunnerEsterni (GIT-278): tabelle dei runner e dei token di registrazione,
// token solo come hash da 32 byte, nome unico, runs_on/runner_id sui job; il down toglie
// tutto e il nuovo up rifunziona.
func TestMigration0033_RunnerEsterni(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	if err := exec(`INSERT INTO core.ci_runner_registration_tokens (id, token_hash, created_at, expires_at)
		VALUES (gen_random_uuid(), decode(repeat('ab', 32), 'hex'), now(), now() + interval '1 hour')`); err != nil {
		t.Fatalf("token di registrazione: %v", err)
	}
	if err := exec(`INSERT INTO core.ci_runner_registration_tokens (id, token_hash, created_at, expires_at)
		VALUES (gen_random_uuid(), decode('abcd', 'hex'), now(), now())`); err == nil {
		t.Error("un hash non da 32 byte doveva essere rifiutato")
	}
	if err := exec(`INSERT INTO core.ci_runners (id, name, labels, token_hash, created_at)
		VALUES (gen_random_uuid(), 'r1', '{self-hosted}', decode(repeat('01', 32), 'hex'), now())`); err != nil {
		t.Fatalf("runner: %v", err)
	}
	if err := exec(`INSERT INTO core.ci_runners (id, name, labels, token_hash, created_at)
		VALUES (gen_random_uuid(), 'r1', '{}', decode(repeat('02', 32), 'hex'), now())`); err == nil {
		t.Error("nome doppio doveva essere rifiutato")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'ci_jobs'
		AND column_name IN ('runs_on', 'runner_id')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("colonne dei job: %d %v", n, err)
	}
	if err := migrate.Down(ctx, pool, dsn, 15); err != nil {
		t.Fatalf("down 0033: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
		AND table_name IN ('ci_runners', 'ci_runner_registration_tokens')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'ci_jobs'
		AND column_name IN ('runs_on', 'runner_id')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("colonne dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
