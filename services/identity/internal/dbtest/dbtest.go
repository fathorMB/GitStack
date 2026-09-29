//go:build integration

// Package dbtest fornisce ai test d'integrazione di identity un Postgres
// reale. Come per internal/migrate, il server lo indica la variabile
// GITSTACK_TEST_DATABASE_URL (per esempio un container
// `postgres:16-alpine`, vedi README); senza variabile il test è saltato.
//
// Ogni chiamata crea un database dedicato (CREATE DATABASE, poi rimosso a
// fine test) e applica le migrazioni di identity, così i pacchetti di test
// possono girare in parallelo sullo stesso server. Se il ruolo non può
// creare database, si ripiega sul database indicato, dove le tabelle dello
// schema identity vengono rimosse e ricreate: in quel caso i pacchetti
// vanno lanciati uno alla volta (go test -p 1).
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool ritorna un pool su un database con lo schema di identity migrato,
// più il DSN (per chi richiama internal/migrate).
func NewPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("GITSTACK_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("GITSTACK_TEST_DATABASE_URL non impostata: test d'integrazione saltato")
	}
	ctx := context.Background()

	dsn := base
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("apertura pool amministrativo: %v", err)
	}
	t.Cleanup(admin.Close)

	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	name := "gs_test_" + hex.EncodeToString(suffix[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err == nil {
		u, perr := url.Parse(base)
		if perr != nil {
			t.Fatalf("DSN non valido: %v", perr)
		}
		u.Path = "/" + name
		dsn = u.String()
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		})
	} else {
		t.Logf("CREATE DATABASE non riuscito (%v): uso il database indicato", err)
		dropTables(ctx, admin)
		t.Cleanup(func() { dropTables(context.Background(), admin) })
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("apertura pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("migrazioni: %v", err)
	}
	return pool, dsn
}

func dropTables(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `DO $$ DECLARE r record; BEGIN
		FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'identity' LOOP
			EXECUTE format('DROP TABLE IF EXISTS identity.%I CASCADE', r.tablename);
		END LOOP; END $$`)
}
