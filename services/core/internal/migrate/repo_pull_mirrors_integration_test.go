//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// 0015 (GIT-195): mirror in pull. Un mirror al massimo per repo, stato e
// modalità tra quelli noti, intervallo tra 5 minuti e 24 ore, credenziali
// tutte o niente, un mirror interrotto senza token; esecuzioni ed elenco dei
// ref divergenti seguono il mirror (e il repo) in cascata; il down toglie le
// tre tabelle e un nuovo up le ricrea.
func TestMigration0015_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	tables := []string{"repo_pull_mirrors", "repo_pull_mirror_runs", "repo_pull_mirror_refs"}
	for _, tbl := range tables {
		if !tableExists(t, pool, tbl) {
			t.Fatalf("%s dovrebbe esistere dopo le migrazioni", tbl)
		}
	}
	repo := newIssueRepo(t, pool, "pullmirror")
	mirror := uuid.New()
	insert := `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, $3, $4)`
	if _, err := pool.Exec(ctx, insert, mirror, repo, "https://github.com/o/r.git", uuid.New()); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	// Default: mirror in coda, ogni 10 minuti, senza credenziali.
	var state, mode string
	var interval int
	var hasToken bool
	if err := pool.QueryRow(ctx, `SELECT state, mode, interval_seconds, token_ciphertext IS NOT NULL FROM core.repo_pull_mirrors WHERE id = $1`, mirror).
		Scan(&state, &mode, &interval, &hasToken); err != nil || state != "pending" || mode != "mirror" || interval != 600 || hasToken {
		t.Fatalf("default: %s %s %d %v (%v)", state, mode, interval, hasToken, err)
	}
	// Un solo mirror in pull per repo.
	_, err := pool.Exec(ctx, insert, uuid.New(), repo, "https://example.org/altro.git", uuid.New())
	wantCode(t, err, "23505")
	// Stato, modalità e intervallo fuori elenco: no.
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'diverged' WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET mode = 'boh' WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET interval_seconds = 60 WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET interval_seconds = 90000 WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	// Credenziali tutte o niente.
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET token_ciphertext = '\x00' WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	if _, err := pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET username = 'u', token_ciphertext = '\x00',
		token_nonce = '\x000000000000000000000000', token_key_id = 'k1' WHERE id = $1`, mirror); err != nil {
		t.Fatalf("credenziali complete: %v", err)
	}
	// Interrotto: serve stopped_at e niente token.
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'stopped' WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'stopped', stopped_at = now() WHERE id = $1`, mirror)
	wantCode(t, err, "23514")
	if _, err := pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'stopped', stopped_at = now(), next_attempt_at = NULL,
		username = NULL, token_ciphertext = NULL, token_nonce = NULL, token_key_id = NULL WHERE id = $1`, mirror); err != nil {
		t.Fatalf("interruzione: %v", err)
	}

	// Esecuzioni: solo esiti e cause noti.
	run := `INSERT INTO core.repo_pull_mirror_runs (id, mirror_id, trigger, started_at, finished_at, outcome) VALUES ($1, $2, $3, now(), now(), $4)`
	if _, err := pool.Exec(ctx, run, uuid.New(), mirror, "initial", "diverged"); err != nil {
		t.Fatalf("run: %v", err)
	}
	_, err = pool.Exec(ctx, run, uuid.New(), mirror, "schedule", "boh")
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, run, uuid.New(), mirror, "boh", "success")
	wantCode(t, err, "23514")

	// Ref divergenti: uno per ref, `deleted` senza SHA remoto, `rewritten` con.
	ref := `INSERT INTO core.repo_pull_mirror_refs (mirror_id, ref, kind, local_sha, remote_sha) VALUES ($1, $2, $3, 'abcdef0', $4)`
	if _, err := pool.Exec(ctx, ref, mirror, "refs/heads/main", "rewritten", "1234567"); err != nil {
		t.Fatalf("ref rewritten: %v", err)
	}
	if _, err := pool.Exec(ctx, ref, mirror, "refs/tags/v1", "deleted", nil); err != nil {
		t.Fatalf("ref deleted: %v", err)
	}
	_, err = pool.Exec(ctx, ref, mirror, "refs/heads/main", "rewritten", "7654321")
	wantCode(t, err, "23505")
	_, err = pool.Exec(ctx, ref, mirror, "refs/heads/x", "deleted", "1234567")
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, ref, mirror, "refs/heads/y", "rewritten", nil)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, ref, mirror, "refs/heads/z", "boh", "1234567")
	wantCode(t, err, "23514")

	// Un mirror eliminato porta via esecuzioni e ref.
	if _, err := pool.Exec(ctx, `DELETE FROM core.repo_pull_mirrors WHERE id = $1`, mirror); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.repo_pull_mirror_runs) + (SELECT count(*) FROM core.repo_pull_mirror_refs)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("righe orfane dopo la cancellazione del mirror: %d (%v)", n, err)
	}
	// Anche l'eliminazione del repo porta via il mirror.
	m2 := uuid.New()
	if _, err := pool.Exec(ctx, insert, m2, repo, "https://github.com/o/r2.git", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ref, m2, "refs/heads/main", "rewritten", "1234567"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM core.repositories WHERE resource_id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.repo_pull_mirrors) + (SELECT count(*) FROM core.repo_pull_mirror_refs)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("righe orfane dopo la cancellazione del repo: %d (%v)", n, err)
	}

	// Down: via le tre tabelle, non il mirror in push; poi un nuovo up.
	if err := migrate.Down(ctx, pool, dsn, 9); err != nil {
		t.Fatalf("down 0015: %v", err)
	}
	for _, tbl := range tables {
		if tableExists(t, pool, tbl) {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	if !tableExists(t, pool, "repo_mirrors") {
		t.Fatal("repo_mirrors (0014) deve restare dopo il down di 0015")
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if !tableExists(t, pool, "repo_pull_mirrors") {
		t.Fatal("repo_pull_mirrors dopo il nuovo up")
	}
}
