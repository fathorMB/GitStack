//go:build integration

package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var prModelTables = []string{"pull_request_reviews", "pull_request_threads", "pull_request_comments",
	"pull_request_review_requests", "commit_statuses", "branch_settings", "pull_request_viewed_files"}

const sha1 = "0123456789abcdef0123456789abcdef01234567"

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// 0018 (GIT-259): su un database con dati della v1.0 (issues e PR con lo
// stesso contatore) il down toglie le tabelle nuove senza toccare issues e
// PR, e l'up le riporta; i dati restano e il contatore non si sposta.
func TestMigration0018_DatiV10_SuGiuSu(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	// Stato v1.0: schema alla 0017.
	if err := migrate.Down(ctx, pool, dsn, 16); err != nil {
		t.Fatalf("down 0018: %v", err)
	}
	for _, tbl := range prModelTables {
		if tableExists(t, pool, tbl) {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	repo := newIssueRepo(t, pool, "v10")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n1, err1 := insertIssue(ctx, tx, repo, "i")
	n2, err2 := insertPR(ctx, tx, repo)
	n3, err3 := insertIssue(ctx, tx, repo, "j")
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("dati v1.0: %v %v %v", err1, err2, err3)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n1 != 1 || n2 != 2 || n3 != 3 {
		t.Fatalf("numeri attesi 1,2,3, ottenuti %d,%d,%d", n1, n2, n3)
	}

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0018 con dati v1.0: %v", err)
	}
	for _, tbl := range prModelTables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo l'up", tbl)
		}
	}
	var prs, issues, next int64
	var draft bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.pull_requests), (SELECT count(*) FROM core.issues),
		(SELECT next_number FROM core.repo_counters WHERE repo_id = $1), (SELECT draft FROM core.pull_requests LIMIT 1)`, repo).
		Scan(&prs, &issues, &next, &draft); err != nil {
		t.Fatal(err)
	}
	if prs != 1 || issues != 2 || next != 4 || draft {
		t.Fatalf("dopo l'up: prs=%d issues=%d next=%d draft=%v", prs, issues, next, draft)
	}
	// Il numero resta unico fra issues e PR anche con le colonne nuove.
	_, err = pool.Exec(ctx, `INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 2, 'x', $3)`,
		uuid.New(), repo, uuid.New())
	wantCode(t, err, "23505")

	// Giù e su ancora: i dati v1.0 sopravvivono al down.
	if err := migrate.Down(ctx, pool, dsn, 16); err != nil {
		t.Fatalf("secondo down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.pull_requests), (SELECT count(*) FROM core.issues)`).
		Scan(&prs, &issues); err != nil || prs != 1 || issues != 2 {
		t.Fatalf("dati dopo il down: prs=%d issues=%d err=%v", prs, issues, err)
	}
	var hasDraft bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema='core' AND table_name='pull_requests' AND column_name IN ('draft','head_sha','merge_method'))`).
		Scan(&hasDraft); err != nil || hasDraft {
		t.Fatalf("colonne nuove rimaste dopo il down: %v err=%v", hasDraft, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("secondo up: %v", err)
	}
}

func TestMigration0018_Vincoli(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "model")
	user := uuid.New()

	tx, _ := pool.Begin(ctx)
	if _, err := insertPR(ctx, tx, repo); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var pr uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM core.pull_requests WHERE repo_id = $1`, repo).Scan(&pr); err != nil {
		t.Fatal(err)
	}

	// Un PR non unita non ha dettagli di merge; unita non e' bozza.
	_, err := pool.Exec(ctx, `UPDATE core.pull_requests SET merge_method = 'squash' WHERE id = $1`, pr)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.pull_requests SET state='merged', draft=true, merged_at=now() WHERE id = $1`, pr)
	wantCode(t, err, "23514")
	mustExec(t, pool, `UPDATE core.pull_requests SET state='merged', merge_method='merge', merged_at=now(),
		base_sha=$2, head_sha=$2 WHERE id = $1`, pr, sha1)
	_, err = pool.Exec(ctx, `UPDATE core.pull_requests SET merge_method='rebase' WHERE id = $1`, pr)
	wantCode(t, err, "23514")

	// Review: stato valido, flag da agente.
	rev := uuid.New()
	mustExec(t, pool, `INSERT INTO core.pull_request_reviews (id, pull_request_id, author_id, state, commit_sha, by_agent)
		VALUES ($1, $2, $3, 'approve', $4, true)`, rev, pr, user, sha1)
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_reviews (id, pull_request_id, author_id, state, commit_sha)
		VALUES ($1, $2, $3, 'approved', $4)`, uuid.New(), pr, user, sha1)
	wantCode(t, err, "23514")

	// Conversazione sulle righe e commenti; risolta richiede resolved_at.
	th := uuid.New()
	mustExec(t, pool, `INSERT INTO core.pull_request_threads (id, pull_request_id, review_id, commit_sha, path, start_line, line)
		VALUES ($1, $2, $3, $4, 'a/b.go', 3, 5)`, th, pr, rev, sha1)
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_threads (id, pull_request_id, commit_sha, path, start_line, line)
		VALUES ($1, $2, $3, 'a.go', 9, 5)`, uuid.New(), pr, sha1)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.pull_request_threads SET resolved = true WHERE id = $1`, th)
	wantCode(t, err, "23514")
	mustExec(t, pool, `UPDATE core.pull_request_threads SET resolved = true, resolved_by = $2, resolved_at = now(), outdated = true WHERE id = $1`, th, user)
	mustExec(t, pool, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body) VALUES ($1, $2, $3, $4, 'ok')`, uuid.New(), th, pr, user)

	// Revisori richiesti: uno per utente.
	mustExec(t, pool, `INSERT INTO core.pull_request_review_requests (pull_request_id, reviewer_id, requested_by) VALUES ($1, $2, $3)`, pr, user, user)
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_review_requests (pull_request_id, reviewer_id, requested_by) VALUES ($1, $2, $3)`, pr, user, user)
	wantCode(t, err, "23505")

	// Stati dei commit: uno per (repo, sha, contesto), aggiornato all'ultimo.
	up := `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (repo_id, sha, context) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`
	mustExec(t, pool, up, uuid.New(), repo, sha1, "ci/build", "pending", user)
	mustExec(t, pool, up, uuid.New(), repo, sha1, "ci/build", "success", user)
	mustExec(t, pool, up, uuid.New(), repo, sha1, "ci/lint", "failure", user)
	var cnt int
	var state string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(state) FILTER (WHERE context = 'ci/build') FROM core.commit_statuses WHERE repo_id = $1`, repo).
		Scan(&cnt, &state); err != nil || cnt != 2 || state != "success" {
		t.Fatalf("stati: %d %q err=%v", cnt, state, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id)
		VALUES ($1, $2, $3, 'ci/build', 'success', $4)`, uuid.New(), repo, sha1, user)
	wantCode(t, err, "23505")
	_, err = pool.Exec(ctx, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id)
		VALUES ($1, $2, 'xyz', 'c', 'success', $3)`, uuid.New(), repo, user)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.commit_statuses (id, repo_id, sha, context, state, creator_id)
		VALUES ($1, $2, $3, 'c', 'ok', $4)`, uuid.New(), repo, sha1, user)
	wantCode(t, err, "23514")

	// Impostazioni del branch principale.
	mustExec(t, pool, `INSERT INTO core.branch_settings (repo_id, require_pull_request, required_approvals, required_contexts, merge_methods)
		VALUES ($1, true, 5, ARRAY['ci/build'], ARRAY['squash'])`, repo)
	for _, bad := range []string{
		"required_approvals = 0", "required_approvals = 6",
		"merge_methods = '{}'", "merge_methods = ARRAY['rebase']", "merge_methods = ARRAY['merge','merge']",
	} {
		_, err = pool.Exec(ctx, `UPDATE core.branch_settings SET `+bad+` WHERE repo_id = $1`, repo)
		wantCode(t, err, "23514")
	}
	mustExec(t, pool, `UPDATE core.branch_settings SET merge_methods = ARRAY['squash','merge'], delete_branch_on_merge = true WHERE repo_id = $1`, repo)

	// Viewed per utente e file.
	mustExec(t, pool, `INSERT INTO core.pull_request_viewed_files (pull_request_id, user_id, path, viewed_sha) VALUES ($1, $2, 'a.go', $3)`, pr, user, sha1)
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_viewed_files (pull_request_id, user_id, path, viewed_sha) VALUES ($1, $2, 'a.go', $3)`, pr, user, sha1)
	wantCode(t, err, "23505")
	mustExec(t, pool, `INSERT INTO core.pull_request_viewed_files (pull_request_id, user_id, path, viewed_sha) VALUES ($1, $2, 'a.go', $3)`, pr, uuid.New(), strings.Repeat("a", 40))

	// Eliminare la PR porta via tutto quello che le appartiene.
	mustExec(t, pool, `DELETE FROM core.pull_requests WHERE id = $1`, pr)
	for _, q := range []string{"pull_request_reviews", "pull_request_threads", "pull_request_comments",
		"pull_request_review_requests", "pull_request_viewed_files"} {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+q).Scan(&cnt); err != nil || cnt != 0 {
			t.Fatalf("%s dopo la cancellazione della PR: %d err=%v", q, cnt, err)
		}
	}
}
