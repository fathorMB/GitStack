//go:build integration

package notify

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/google/uuid"
)

// domainNoticeIdentity: tutti leggono, e gli admin del repo (compresi quelli di sistema, per le regole
// di HasRole) sono quelli dati.
type domainNoticeIdentity struct {
	appNoticeIdentity
	admins []uuid.UUID
}

func (i domainNoticeIdentity) ResourceMembers(context.Context, uuid.UUID, string) ([]uuid.UUID, error) {
	return i.admins, nil
}

// GIT-461: le tre notifiche arrivano a tutti gli admin, una per evento.
func TestAppDomainNotices_TreNotificheAgliAdmin(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	repoID, repoAdmin, sysAdmin := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/web')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name)
		VALUES ($1, 'user', $2, 'web')`, repoID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Pool: pool, Identity: domainNoticeIdentity{admins: []uuid.UUID{repoAdmin, sysAdmin}}}
	events := []struct {
		name string
		p    map[string]any
	}{
		{"app_domain.dns_failed", map[string]any{"reason": "non risolve"}},
		{"app_certificate.renewal_failed", map[string]any{"reason": "porta 80 non raggiungibile"}},
		{"app_certificate.expiring", map[string]any{"validUntil": "2026-11-10T00:00:00Z"}},
	}
	for _, ev := range events {
		ev.p["repo"] = map[string]string{"id": repoID.String()}
		ev.p["appName"] = "web"
		ev.p["domain"] = "www.example.org"
		raw, err := json.Marshal(ev.p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES ($1, $2, 1, $3)`, uuid.New(), ev.name, raw); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := e.Process(ctx); err != nil || n != 3 {
		t.Fatalf("Process = (%d, %v), voluto (3, nil)", n, err)
	}
	for _, ev := range events {
		for _, u := range []uuid.UUID{repoAdmin, sysAdmin} {
			var n int
			var dom string
			if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(max(data->>'domain'), '') FROM core.notifications
				WHERE user_id = $1 AND repo_id = $2 AND event_name = $3`, u, repoID, ev.name).Scan(&n, &dom); err != nil {
				t.Fatal(err)
			}
			if n != 1 || dom != "www.example.org" {
				t.Errorf("%s per %s: %d notifiche (dominio %q), volute 1", ev.name, u, n, dom)
			}
		}
	}
	// Riprocessare non duplica: l'evento è già elaborato.
	if n, err := e.Process(ctx); err != nil || n != 0 {
		t.Fatalf("secondo giro = (%d, %v)", n, err)
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE repo_id = $1`, repoID).Scan(&total); err != nil || total != 6 {
		t.Fatalf("notifiche totali = %d (%v), volute 6", total, err)
	}
}
