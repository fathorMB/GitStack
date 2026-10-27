//go:build integration

package notify

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
)

type appNoticeIdentity struct{}

func (appNoticeIdentity) HasRole(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return true, nil
}

func (appNoticeIdentity) ResolveMentions(context.Context, []string) (identityclient.Mentions, error) {
	return identityclient.Mentions{}, nil
}

func TestAppUnhealthyNotice_RiparteDopoLaFinestra(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	repoID, actorID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/web')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name)
		VALUES ($1, 'user', $2, 'web')`, repoID, uuid.New()); err != nil {
		t.Fatal(err)
	}

	e := &Engine{
		Pool:     pool,
		Identity: appNoticeIdentity{},
	}
	payload, err := json.Marshal(map[string]any{
		"repo":    map[string]string{"id": repoID.String()},
		"actor":   map[string]string{"id": actorID.String()},
		"appName": "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	addEvent := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload)
			VALUES ($1, 'app_status.unhealthy', 1, $2)`, uuid.New(), payload); err != nil {
			t.Fatal(err)
		}
	}
	process := func() {
		t.Helper()
		if n, err := e.Process(ctx); err != nil || n != 1 {
			t.Fatalf("Process = (%d, %v), voluto (1, nil)", n, err)
		}
	}
	countNotices := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications
			WHERE user_id = $1 AND repo_id = $2 AND event_name = 'app_status.unhealthy'`, actorID, repoID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	addEvent()
	process()
	if got := countNotices(); got != 1 {
		t.Fatalf("primo unhealthy: notifiche = %d, voluto 1", got)
	}

	addEvent()
	process()
	if got := countNotices(); got != 1 {
		t.Fatalf("unhealthy dentro la finestra: notifiche = %d, voluto 1", got)
	}

	if _, err := pool.Exec(ctx, `UPDATE core.notifications
		SET created_at = created_at - interval '6 minutes'
		WHERE user_id = $1 AND event_name = 'app_status.unhealthy'`, actorID); err != nil {
		t.Fatal(err)
	}
	addEvent()
	process()
	if got := countNotices(); got != 2 {
		t.Fatalf("unhealthy dopo la finestra: notifiche = %d, voluto 2", got)
	}
}
