//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0023 (GIT-303): via_token_id, via_token_name e by_agent su commit_statuses.
// Le righe esistenti restano valide (senza token, by_agent=false); il down
// toglie le colonne senza perdere le righe.
func TestMigration0023_StatiViaToken(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "statuses")
	user := uuid.New()
	sha := "0123456789abcdef0123456789abcdef01234567"

	cols := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'core' AND table_name = 'commit_statuses'
			  AND column_name IN ('via_token_id', 'via_token_name', 'by_agent')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := cols(); n != 3 {
		t.Fatalf("colonne nuove = %d, volute 3", n)
	}
	tok := uuid.New()
	mustExec(t, pool, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id, via_token_id, via_token_name, by_agent)
		VALUES ($1, $2, $3, 'ci/a', 'success', $4, $5, 'ci-runner', true)`, uuid.New(), repo, sha, user, tok)
	mustExec(t, pool, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id) VALUES ($1, $2, $3, 'ci/b', 'success', $4)`,
		uuid.New(), repo, sha, user)
	var nullTok, notAgent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE via_token_id IS NULL AND via_token_name IS NULL),
			count(*) FILTER (WHERE NOT by_agent) FROM core.commit_statuses WHERE context = 'ci/b'`).Scan(&nullTok, &notAgent); err != nil || nullTok != 1 || notAgent != 1 {
		t.Fatalf("default di una riga senza token: %d %d (%v)", nullTok, notAgent, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 27); err != nil {
		t.Fatalf("down 0023: %v", err)
	}
	if n := cols(); n != 0 {
		t.Fatalf("colonne dopo il down = %d", n)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.commit_statuses`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("righe dopo il down = %d (%v)", rows, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0023 dopo down: %v", err)
	}
	if n := cols(); n != 3 {
		t.Fatalf("colonne dopo il nuovo up = %d", n)
	}
}
