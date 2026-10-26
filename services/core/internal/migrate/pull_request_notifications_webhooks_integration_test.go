//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0022 (GIT-265): notifiche e webhook delle PR. Su: tabelle di iscrizione e
// di chiusura, colonna has_conflicts, motivo review_requested, tre webhook
// nuovi. Giù con dati presenti: le notifiche e i webhook solo-PR spariscono,
// quelli misti restano con gli eventi vecchi, il resto è intatto.
func TestMigration0022_NotificheEWebhookDelleRichieste(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo := newIssueRepo(t, pool, "notif22")
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

	// Iscrizioni: motivo ammesso, motivo sconosciuto rifiutato, una riga per utente.
	mustExec(t, pool, `INSERT INTO core.pull_request_subscriptions (pull_request_id, user_id, reason) VALUES ($1, $2, 'reviewer')`, pr, user)
	_, err := pool.Exec(ctx, `INSERT INTO core.pull_request_subscriptions (pull_request_id, user_id, reason) VALUES ($1, $2, 'author')`, pr, user)
	wantCode(t, err, "23505")
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_subscriptions (pull_request_id, user_id, reason) VALUES ($1, $2, 'boh')`, pr, uuid.New())
	wantCode(t, err, "23514")

	// Notifica di una PR col nuovo motivo; senza repo non si può.
	nid := uuid.New()
	mustExec(t, pool, `INSERT INTO core.notifications (id, user_id, reason, repo_id, pull_request_id, event_name)
		VALUES ($1, $2, 'review_requested', $3, $4, 'pull_request.review_requested')`, nid, user, repo, pr)
	_, err = pool.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, pull_request_id) VALUES ($1, $2, 'mentioned', $3)`, uuid.New(), user, pr)
	wantCode(t, err, "23514")
	mustExec(t, pool, `INSERT INTO core.notification_preferences (user_id, reason, email_enabled) VALUES ($1, 'review_requested', false)`, user)

	// Collegamento di chiusura (PR8): chiave (PR, issue), parola ammessa.
	issue := uuid.New()
	mustExec(t, pool, `INSERT INTO core.issues (id, repo_id, number, title, author_id) VALUES ($1, $2, 99, 'x', $3)`, issue, repo, user)
	mustExec(t, pool, `INSERT INTO core.pull_request_closing_issues (pull_request_id, issue_id, keyword) VALUES ($1, $2, 'fix')`, pr, issue)
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_closing_issues (pull_request_id, issue_id, keyword) VALUES ($1, $2, 'fix')`, pr, issue)
	wantCode(t, err, "23505")
	_, err = pool.Exec(ctx, `INSERT INTO core.pull_request_closing_issues (pull_request_id, issue_id, keyword) VALUES ($1, $2, 'boh')`, pr, uuid.New())
	wantCode(t, err, "23514")

	// Webhook: i tre eventi nuovi sono ammessi, uno inventato no.
	solo, misto, vecchio := uuid.New(), uuid.New(), uuid.New()
	for id, evs := range map[uuid.UUID]string{
		solo:    `ARRAY['pull_request','commit_status']`,
		misto:   `ARRAY['issues','pull_request_review']`,
		vecchio: `ARRAY['push']`,
	} {
		mustExec(t, pool, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by) VALUES ($1, 'repo', $2, 'https://example.test/h', `+evs+`::text[], $3)`, id, repo, user)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by) VALUES ($1, 'repo', $2, 'https://example.test/h', ARRAY['pull_requests'], $3)`, uuid.New(), repo, user)
	wantCode(t, err, "23514")
	mustExec(t, pool, `INSERT INTO core.webhook_deliveries (id, webhook_id, event, action, payload) VALUES ($1, $2, 'pull_request', 'opened', '{}')`, uuid.New(), solo)
	mustExec(t, pool, `INSERT INTO core.webhook_deliveries (id, webhook_id, event, action, payload) VALUES ($1, $2, 'issues', 'opened', '{}')`, uuid.New(), misto)

	// Giù.
	if err := migrate.Down(ctx, pool, dsn, 20); err != nil {
		t.Fatalf("down 0022 con dati presenti: %v", err)
	}
	var notifs, prefs, webhooks, deliveries, cols, tables int
	var mistoEvents []string
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.notifications WHERE id = $1),
			(SELECT count(*) FROM core.notification_preferences WHERE user_id = $2),
			(SELECT count(*) FROM core.webhooks), (SELECT count(*) FROM core.webhook_deliveries),
			(SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core'
				AND ((table_name = 'notifications' AND column_name = 'pull_request_id')
				  OR (table_name = 'pull_requests' AND column_name = 'has_conflicts'))),
			(SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
				AND table_name IN ('pull_request_subscriptions', 'pull_request_closing_issues'))`, nid, user).
		Scan(&notifs, &prefs, &webhooks, &deliveries, &cols, &tables); err != nil {
		t.Fatal(err)
	}
	if notifs != 0 || prefs != 0 || webhooks != 2 || deliveries != 1 || cols != 0 || tables != 0 {
		t.Fatalf("dopo il down: notifiche=%d preferenze=%d webhook=%d consegne=%d colonne=%d tabelle=%d", notifs, prefs, webhooks, deliveries, cols, tables)
	}
	if err := pool.QueryRow(ctx, `SELECT events FROM core.webhooks WHERE id = $1`, misto).Scan(&mistoEvents); err != nil {
		t.Fatal(err)
	}
	if len(mistoEvents) != 1 || mistoEvents[0] != "issues" {
		t.Fatalf("eventi del webhook misto dopo il down = %v", mistoEvents)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by) VALUES ($1, 'repo', $2, 'https://example.test/h', ARRAY['commit_status'], $3)`, uuid.New(), repo, user)
	wantCode(t, err, "23514")

	// Su ancora.
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0022: %v", err)
	}
	mustExec(t, pool, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by) VALUES ($1, 'repo', $2, 'https://example.test/h', ARRAY['commit_status'], $3)`, uuid.New(), repo, user)
}
