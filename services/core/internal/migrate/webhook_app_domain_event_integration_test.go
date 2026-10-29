//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0054_WebhookAppDomainEvent (GIT-492, v3-c): il webhook app_domain è un evento
// selezionabile dei webhook del repo; un evento sconosciuto resta rifiutato; il down riporta
// l'elenco della 0044 e toglie il nuovo evento dai webhook che lo avevano.
func TestMigration0054_WebhookAppDomainEvent(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/w')`, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'w')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	insert := func(events ...string) (uuid.UUID, error) {
		id := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by)
			VALUES ($1, 'repo', $2, 'https://hooks.example.com/x', $3, $4)`, id, repo, events, owner)
		return id, err
	}
	retained, err := insert("app_domain", "deployment")
	if err != nil {
		t.Fatalf("il webhook app_domain deve essere accettato: %v", err)
	}
	singleEvent, err := insert("app_domain")
	if err != nil {
		t.Fatalf("il webhook con il solo evento app_domain deve essere accettato: %v", err)
	}
	if _, err := insert("app_domains"); err == nil {
		t.Error("un evento sconosciuto deve essere rifiutato")
	}

	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0054: %v", err)
	}
	if _, err := insert("app_domain"); err == nil {
		t.Error("dopo il down 0054 il webhook app_domain non è più ammesso")
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.webhooks WHERE id = $1)`, singleEvent).Scan(&exists); err != nil || exists {
		t.Fatalf("il down deve eliminare il webhook iscritto solo a app_domain: exists=%v err=%v", exists, err)
	}
	var events []string
	if err := pool.QueryRow(ctx, `SELECT events FROM core.webhooks WHERE id = $1`, retained).Scan(&events); err != nil {
		t.Fatalf("il webhook con più eventi deve restare: %v", err)
	}
	if len(events) != 1 || events[0] != "deployment" {
		t.Fatalf("il webhook deve conservare solo deployment, eventi: %v", events)
	}
	if _, err := insert("deployment"); err != nil {
		t.Fatalf("deployment resta ammesso dopo il down 0054: %v", err)
	}
}
