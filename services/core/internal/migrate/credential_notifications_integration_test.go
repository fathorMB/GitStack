//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0017 (GIT-239, F13): il motivo `credential` è ammesso senza repo né issue;
// il down toglie le notifiche sulle credenziali e rimette il vincolo di 0014.
func TestMigration0017_NotificheSulleCredenziali(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	ins := `INSERT INTO core.notifications (id, user_id, reason, event_name, data) VALUES ($1, $2, $3, 'credential.password_changed', '{"by":"alice"}')`
	user := uuid.New()
	if _, err := pool.Exec(ctx, ins, uuid.New(), user, "credential"); err != nil {
		t.Fatalf("notifica sulle credenziali: %v", err)
	}
	_, err := pool.Exec(ctx, ins, uuid.New(), user, "inventato")
	wantCode(t, err, "23514")

	if err := migrate.Down(ctx, pool, dsn, 22); err != nil {
		t.Fatalf("down 0017: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE reason = 'credential'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("notifiche sulle credenziali dopo il down: %d (%v)", n, err)
	}
	_, err = pool.Exec(ctx, ins, uuid.New(), user, "credential")
	wantCode(t, err, "23514")
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
