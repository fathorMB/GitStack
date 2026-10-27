//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// TestMigration0044_WebhookDeploymentEvent (GIT-384, H12): il webhook deployment è un evento
// selezionabile dei webhook del repo; un evento sconosciuto resta rifiutato; il down riporta
// l'elenco della 0030 e toglie il nuovo evento dai webhook che lo avevano.
func TestMigration0044_WebhookDeploymentEvent(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	repo, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/w')`, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'w')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	insert := func(events ...string) error {
		_, err := pool.Exec(ctx, `INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by)
			VALUES ($1, 'repo', $2, 'https://hooks.example.com/x', $3, $4)`, uuid.New(), repo, events, owner)
		return err
	}
	if err := insert("deployment", "push"); err != nil {
		t.Fatalf("il webhook deployment deve essere accettato: %v", err)
	}
	if err := insert("deploy"); err == nil {
		t.Error("un evento sconosciuto deve essere rifiutato")
	}

	if err := migrate.Down(ctx, pool, dsn, 3); err != nil {
		t.Fatalf("down 0044: %v", err)
	}
	if err := insert("deployment"); err == nil {
		t.Error("dopo il down 0044 il webhook deployment non è più ammesso")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.webhooks WHERE 'deployment' = ANY(events)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("il down deve togliere deployment dai webhook: %d %v", n, err)
	}
}
