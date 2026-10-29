//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0016 (GIT-197): le notifiche `mirror` possono riferirsi a un mirror in pull
// (pull_mirror_id) invece che a uno in push (mirror_id): esattamente uno dei
// due, solo con quel motivo; seguono il mirror in cascata; il down toglie le
// notifiche del mirror in pull e la colonna, e rimette il vincolo di 0014.
func TestMigration0016_NotificheDelMirrorInPull(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	repo := newIssueRepo(t, pool, "pullnotif")
	pull, push := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, 'https://github.com/o/r.git', $3)`,
		pull, repo, uuid.New()); err != nil {
		t.Fatalf("mirror in pull: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_mirrors (id, repo_id, url, username, token_ciphertext, token_nonce, token_key_id, created_by)
		VALUES ($1, $2, 'https://github.com/o/push.git', 'u', '\x00', '\x000000000000000000000000', 'k1', $3)`, push, repo, uuid.New()); err != nil {
		t.Fatalf("mirror in push: %v", err)
	}
	notif := `INSERT INTO core.notifications (id, user_id, reason, repo_id, mirror_id, pull_mirror_id) VALUES ($1, $2, $3, $4, $5, $6)`
	user := uuid.New()
	if _, err := pool.Exec(ctx, notif, uuid.New(), user, "mirror", repo, nil, pull); err != nil {
		t.Fatalf("notifica del mirror in pull: %v", err)
	}
	if _, err := pool.Exec(ctx, notif, uuid.New(), user, "mirror", repo, push, nil); err != nil {
		t.Fatalf("notifica del mirror in push: %v", err)
	}
	// Né nessuno né tutti e due; né un altro motivo con un mirror.
	_, err := pool.Exec(ctx, notif, uuid.New(), user, "mirror", repo, nil, nil)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, notif, uuid.New(), user, "mirror", repo, push, pull)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, notif, uuid.New(), user, "mentioned", repo, nil, pull)
	wantCode(t, err, "23514")

	// Il mirror in pull eliminato porta via le sue notifiche, non le altre.
	if _, err := pool.Exec(ctx, `DELETE FROM core.repo_pull_mirrors WHERE id = $1`, pull); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE reason = 'mirror'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifiche dopo la cancellazione: %d (%v)", n, err)
	}

	// Down: via le notifiche del mirror in pull e la colonna; il vincolo di 0014 torna.
	pull2 := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, 'https://github.com/o/r2.git', $3)`,
		pull2, repo, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, notif, uuid.New(), user, "mirror", repo, nil, pull2); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Down(ctx, pool, dsn, 39); err != nil {
		t.Fatalf("down 0016: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE reason = 'mirror'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifiche dopo il down: %d (%v)", n, err)
	}
	var has bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'notifications' AND column_name = 'pull_mirror_id')`).Scan(&has); err != nil || has {
		t.Fatalf("pull_mirror_id dopo il down: %v (%v)", has, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
