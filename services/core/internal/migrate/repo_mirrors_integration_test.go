//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0013 (GIT-179): mirror in push. Vincoli (stato, unicità di repo+url, token
// cifrato obbligatorio), notifica `mirror` legata al mirror, cascata
// dall'eliminazione del repo e down che toglie tutto (anche le notifiche
// `mirror` e il vincolo sui motivi) prima di un nuovo up.
func TestMigration0013_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	for _, tbl := range []string{"repo_mirrors", "repo_mirror_runs"} {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo le migrazioni", tbl)
		}
	}
	repo := newIssueRepo(t, pool, "mirror")
	mirror := uuid.New()
	insert := `INSERT INTO core.repo_mirrors (id, repo_id, url, username, token_ciphertext, token_nonce, token_key_id, created_by)
		VALUES ($1, $2, $3, 'u', '\x00', '\x000000000000000000000000', 'k1', $4)`
	if _, err := pool.Exec(ctx, insert, mirror, repo, "https://github.com/o/r.git", uuid.New()); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	// Default: in coda, subito.
	var state string
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT state, pending FROM core.repo_mirrors WHERE id = $1`, mirror).Scan(&state, &pending); err != nil || state != "pending" || !pending {
		t.Fatalf("default: %s %v (%v)", state, pending, err)
	}
	// Stesso repo e stesso url due volte: no (unique).
	_, err := pool.Exec(ctx, insert, uuid.New(), repo, "https://github.com/o/r.git", uuid.New())
	wantCode(t, err, "23505")
	// Stato fuori elenco: no.
	_, err = pool.Exec(ctx, `UPDATE core.repo_mirrors SET state = 'boh' WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	// Il token è obbligatorio.
	_, err = pool.Exec(ctx, `INSERT INTO core.repo_mirrors (id, repo_id, url, username, token_ciphertext, token_nonce, created_by)
		VALUES ($1, $2, 'https://x.example/a.git', 'u', '\x00', '\x00', $3)`, uuid.New(), repo, uuid.New())
	wantCode(t, err, "23502")
	// Esito di un'esecuzione: solo quelli noti.
	run := `INSERT INTO core.repo_mirror_runs (id, mirror_id, started_at, finished_at, outcome) VALUES ($1, $2, now(), now(), $3)`
	if _, err := pool.Exec(ctx, run, uuid.New(), mirror, "diverged"); err != nil {
		t.Fatalf("run: %v", err)
	}
	_, err = pool.Exec(ctx, run, uuid.New(), mirror, "boh")
	wantCode(t, err, "23514")

	// Notifica `mirror`: solo con mirror_id, e mirror_id solo con quel motivo.
	notif := `INSERT INTO core.notifications (id, user_id, reason, repo_id, mirror_id) VALUES ($1, $2, $3, $4, $5)`
	if _, err := pool.Exec(ctx, notif, uuid.New(), uuid.New(), "mirror", repo, mirror); err != nil {
		t.Fatalf("notifica mirror: %v", err)
	}
	_, err = pool.Exec(ctx, notif, uuid.New(), uuid.New(), "mirror", repo, nil)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, notif, uuid.New(), uuid.New(), "mentioned", repo, mirror)
	wantCode(t, err, "23514")
	// E una preferenza email per il nuovo tipo.
	if _, err := pool.Exec(ctx, `INSERT INTO core.notification_preferences (user_id, reason, email_enabled) VALUES ($1, 'mirror', true)`, uuid.New()); err != nil {
		t.Fatalf("preferenza mirror: %v", err)
	}

	// Un mirror eliminato porta via log e notifiche.
	if _, err := pool.Exec(ctx, `DELETE FROM core.repo_mirrors WHERE id = $1`, mirror); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.repo_mirror_runs) + (SELECT count(*) FROM core.notifications WHERE reason = 'mirror')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("righe orfane dopo la cancellazione del mirror: %d (%v)", n, err)
	}

	// Down: via tabelle, colonna, notifiche e preferenze `mirror`; il resto resta.
	m2 := uuid.New()
	if _, err := pool.Exec(ctx, insert, m2, repo, "https://github.com/o/r2.git", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, notif, uuid.New(), uuid.New(), "mirror", repo, m2); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, notif, uuid.New(), uuid.New(), "mentioned", repo, nil); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0013: %v", err)
	}
	for _, tbl := range []string{"repo_mirrors", "repo_mirror_runs"} {
		if tableExists(t, pool, tbl) {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE reason = 'mentioned'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("le altre notifiche devono restare: %d (%v)", left, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason) VALUES ($1, $2, 'mirror')`, uuid.New(), uuid.New())
	wantCode(t, err, "23514")
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if !tableExists(t, pool, "repo_mirrors") {
		t.Fatal("repo_mirrors dopo il nuovo up")
	}
}
