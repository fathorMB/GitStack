//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
)

// 0012 (GIT-135): webhooked_at sull'outbox. Gli eventi già presenti si
// segnano elaborati (non generano consegne), i nuovi nascono da elaborare; il
// down toglie colonna e indice e un nuovo up li ricrea.
func TestMigration0012_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0012: %v", err)
	}
	q := `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'event_outbox' AND column_name = 'webhooked_at')`
	idx := `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'event_outbox_unwebhooked')`
	var has, hasIdx bool
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || has {
		t.Fatalf("webhooked_at dopo il down: %v %v", has, err)
	}
	if err := pool.QueryRow(ctx, idx).Scan(&hasIdx); err != nil || hasIdx {
		t.Fatalf("indice dopo il down: %v %v", hasIdx, err)
	}
	// 0010 resta: notified_at c'è ancora.
	var notified bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'event_outbox' AND column_name = 'notified_at')`).Scan(&notified); err != nil || !notified {
		t.Fatalf("notified_at deve restare: %v %v", notified, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0012: %v", err)
	}
	if err := pool.QueryRow(ctx, q).Scan(&has); err != nil || !has {
		t.Fatalf("webhooked_at dopo il up: %v %v", has, err)
	}
	if err := pool.QueryRow(ctx, idx).Scan(&hasIdx); err != nil || !hasIdx {
		t.Fatalf("indice dopo il up: %v %v", hasIdx, err)
	}
	var old, fresh int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NOT NULL`).Scan(&old); err != nil || old != 1 {
		t.Fatalf("eventi preesistenti segnati elaborati = %d (%v)", old, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'issue.created', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NULL`).Scan(&fresh); err != nil || fresh != 1 {
		t.Fatalf("eventi nuovi da elaborare = %d (%v)", fresh, err)
	}
}

// 0013 (GIT-178, R11 rivista): i nomi dei repo conservano le maiuscole e
// l'unicità per owner non distingue maiuscole e minuscole. I repo esistenti
// restano uguali e, poiché il disco è per id, i percorsi non cambiano.
func TestMigration0013_NomiConMaiuscole(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	// Prima della 0013: un repo con nome minuscolo e il nome con maiuscole rifiutato.
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0013: %v", err)
	}
	oldID := newResource(t, pool, "repo", "alice/gitstack")
	if err := insertRepo(pool, oldID, alice, "gitstack"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, insertRepo(pool, newResource(t, pool, "repo", "alice/Maiusc"), alice, "Maiusc"), "23514")

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0013: %v", err)
	}
	// Il repo esistente è invariato: stessa riga, stesso id (e quindi stesso percorso su disco).
	var id uuid.UUID
	var name string
	if err := pool.QueryRow(ctx, `SELECT resource_id, name FROM core.repositories WHERE owner_id = $1`, alice).Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if id != oldID || name != "gitstack" {
		t.Fatalf("repo esistente cambiato: %s %q", id, name)
	}
	// Unicità senza distinzione di maiuscole, per owner; il cestino occupa il nome (R2).
	for _, dup := range []string{"GitStack", "GITSTACK", "gitStack"} {
		wantCode(t, insertRepo(pool, newResource(t, pool, "repo", "alice/"+dup), alice, dup), "23505")
	}
	if _, err := pool.Exec(ctx, `UPDATE core.repositories SET deleted_at = now() WHERE resource_id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, insertRepo(pool, newResource(t, pool, "repo", "alice/GitStack2"), alice, "GITSTACK"), "23505")
	// Altro owner: lo stesso nome è libero e le maiuscole restano come scritte.
	if err := insertRepo(pool, newResource(t, pool, "repo", "bob/GitStack"), bob, "GitStack"); err != nil {
		t.Fatalf("stesso nome con owner diverso: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT name FROM core.repositories WHERE owner_id = $1`, bob).Scan(&name); err != nil || name != "GitStack" {
		t.Fatalf("nome salvato %q (%v)", name, err)
	}
	wantCode(t, insertRepo(pool, newResource(t, pool, "repo", "bob/x.GIT"), bob, "x.GIT"), "23514")

	// Down: senza nomi con maiuscole torna alla regola vecchia.
	if _, err := pool.Exec(ctx, `DELETE FROM core.resources WHERE name = 'bob/GitStack'`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0013 senza maiuscole: %v", err)
	}
	wantCode(t, insertRepo(pool, newResource(t, pool, "repo", "bob/Rifiutato"), bob, "Rifiutato"), "23514")
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0013 dopo down: %v", err)
	}
}
