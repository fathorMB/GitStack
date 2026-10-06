//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var notificationTables = []string{"issue_subscriptions", "repo_watches", "notification_preferences",
	"webhooks", "webhook_deliveries", "notifications", "issue_commit_links", "issue_references"}

func newIssueRow(t *testing.T, pool *pgxpool.Pool, repo uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO core.issues (id, repo_id, number, title, author_id)
		VALUES ($1, $2, 1, 't', $3)`, id, repo, uuid.New()); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMigration0006_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	for _, tbl := range notificationTables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo le migrazioni", tbl)
		}
	}
	repo := newIssueRepo(t, pool, "hooks")
	issue := newIssueRow(t, pool, repo)

	// Gli eventi nuovi della cronologia sono ammessi.
	for _, typ := range []string{"referenced_from", "commit_linked", "closed_by_commit"} {
		if _, err := pool.Exec(ctx, `INSERT INTO core.issue_events (id, issue_id, type) VALUES ($1, $2, $3)`,
			uuid.New(), issue, typ); err != nil {
			t.Fatalf("evento %s: %v", typ, err)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO core.issue_events (id, issue_id, type) VALUES ($1, $2, 'boh')`, uuid.New(), issue)
	wantCode(t, err, "23514")

	// Webhook: scope e destinazione coerenti, segreto tutto o niente, nonce da 12 byte.
	hook := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by,
		secret_ciphertext, secret_nonce, secret_key_id)
		VALUES ($1, 'repo', $2, 'https://example.com/h', ARRAY['push','issues'], $3, '\x00', '\x000000000000000000000000', 'k1')`,
		hook, repo, uuid.New()); err != nil {
		t.Fatalf("webhook di repo: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, org_id, url, events, created_by)
		VALUES ($1, 'org', $2, 'http://example.com/h', ARRAY['repository'], $3)`, uuid.New(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("webhook di organizzazione senza segreto: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, org_id, url, events, created_by)
		VALUES ($1, 'repo', $2, $3, 'https://e.com', ARRAY['push'], $4)`, uuid.New(), repo, uuid.New(), uuid.New())
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by, secret_ciphertext)
		VALUES ($1, 'repo', $2, 'https://e.com', ARRAY['push'], $3, '\x00')`, uuid.New(), repo, uuid.New())
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by)
		VALUES ($1, 'repo', $2, 'https://e.com', ARRAY['star'], $3)`, uuid.New(), repo, uuid.New())
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by)
		VALUES ($1, 'repo', $2, 'ftp://e.com', ARRAY['push'], $3)`, uuid.New(), repo, uuid.New())
	wantCode(t, err, "23514")

	// Consegna: una per evento di dominio e webhook; la redelivery no.
	src := uuid.New()
	ins := func(redeliveryOf any) error {
		_, err := pool.Exec(ctx, `INSERT INTO core.webhook_deliveries (id, webhook_id, event, source_event_id, payload, redelivery_of)
			VALUES ($1, $2, 'push', $3, '{}', $4)`, uuid.New(), hook, src, redeliveryOf)
		return err
	}
	if err := ins(nil); err != nil {
		t.Fatal(err)
	}
	wantCode(t, ins(nil), "23505")
	var first uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM core.webhook_deliveries WHERE webhook_id = $1`, hook).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := ins(first); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	// Notifica: 'webhook' vuole il webhook, le altre no.
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, repo_id, issue_id)
		VALUES ($1, $2, 'assigned', $3, $4)`, uuid.New(), user, repo, issue); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, webhook_id)
		VALUES ($1, $2, 'webhook', $3)`, uuid.New(), user, hook); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason) VALUES ($1, $2, 'webhook')`, uuid.New(), user)
	wantCode(t, err, "23514")

	// Riferimenti e commit collegati: unici.
	sha := "0123456789abcdef0123456789abcdef01234567"
	link := func() error {
		_, err := pool.Exec(ctx, `INSERT INTO core.issue_commit_links (id, issue_id, commit_repo_id, commit_sha, close_keyword)
			VALUES ($1, $2, $3, $4, 'fix')`, uuid.New(), issue, repo, sha)
		return err
	}
	if err := link(); err != nil {
		t.Fatal(err)
	}
	wantCode(t, link(), "23505")
	ref := func() error {
		_, err := pool.Exec(ctx, `INSERT INTO core.issue_references (id, target_issue_id, source_kind, source_repo_id, source_number)
			VALUES ($1, $2, 'issue', $3, 7)`, uuid.New(), issue, repo)
		return err
	}
	if err := ref(); err != nil {
		t.Fatal(err)
	}
	wantCode(t, ref(), "23505")

	// Un repo eliminato porta via webhook, notifiche e watch (cascade).
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_watches (repo_id, user_id, mode) VALUES ($1, $2, 'all')`, repo, user); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.repo_watches (repo_id, user_id, mode) VALUES ($1, $2, 'tutto')`, repo, uuid.New())
	wantCode(t, err, "23514")

	// Down: tabelle via, eventi nuovi eliminati, vincolo precedente di nuovo in vigore.
	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0006: %v", err)
	}
	for _, tbl := range notificationTables {
		if tableExists(t, pool, tbl) {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.issue_events WHERE issue_id = $1`, issue).Scan(&n); err != nil || n != 0 {
		t.Fatalf("eventi rimasti dopo il down: %d err=%v", n, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.issue_events (id, issue_id, type) VALUES ($1, $2, 'commit_linked')`, uuid.New(), issue)
	wantCode(t, err, "23514")

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0006 dopo down: %v", err)
	}
	for _, tbl := range notificationTables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo il nuovo up", tbl)
		}
	}
}
