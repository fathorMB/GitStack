//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func viaTokenColumns(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name IN ('issues', 'issue_comments')
		  AND column_name IN ('via_token_id', 'via_token_name')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 0008 (GIT-125): via_token_id e via_token_name su issues e issue_comments.
func TestMigration0008_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if n := viaTokenColumns(t, pool); n != 4 {
		t.Fatalf("colonne via_token_* = %d, volute 4", n)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0008: %v", err)
	}
	if n := viaTokenColumns(t, pool); n != 0 {
		t.Fatalf("colonne via_token_* dopo il down = %d", n)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0008 dopo down: %v", err)
	}
	if n := viaTokenColumns(t, pool); n != 4 {
		t.Fatalf("colonne via_token_* dopo il nuovo up = %d", n)
	}
}
