//go:build integration

package migrate_test

import (
	"context"
	"os"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Il test usa un Postgres reale indicato da GITSTACK_TEST_DATABASE_URL (per
// esempio `docker run -e POSTGRES_PASSWORD=... postgres:16-alpine`, vedi
// README): si salta se la variabile manca. Ogni test lavora sullo schema
// "identity" e ne rimuove le tabelle prima e dopo.
func newPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("GITSTACK_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GITSTACK_TEST_DATABASE_URL non impostata: test d'integrazione saltato")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("apertura pool: %v", err)
	}
	t.Cleanup(pool.Close)
	// Svuota le tabelle senza droppare lo schema: un ruolo a permessi
	// limitati (bootstrap-role.sql) non potrebbe ricrearlo.
	drop := func() {
		_, _ = pool.Exec(ctx, `DO $$ DECLARE r record; BEGIN
			FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'identity' LOOP
				EXECUTE format('DROP TABLE IF EXISTS identity.%I CASCADE', r.tablename);
			END LOOP; END $$`)
	}
	drop()
	t.Cleanup(drop)
	return pool, dsn
}

func tables(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'identity'`)
	if err != nil {
		t.Fatalf("query tabelle: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got[n] = true
	}
	return got
}

var domainTables = []string{
	"users", "credentials", "sessions", "api_tokens", "ssh_keys",
	"oidc_providers", "oidc_identities", "organizations", "org_members",
	"teams", "team_members", "resource_grants",
}

func TestUpDownUp(t *testing.T) {
	pool, dsn := newPool(t)
	ctx := context.Background()

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("Up idempotente: %v", err)
	}
	got := tables(t, pool)
	for _, n := range domainTables {
		if !got[n] {
			t.Errorf("tabella identity.%s mancante dopo Up", n)
		}
	}

	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("Down: %v", err)
	}
	got = tables(t, pool)
	for _, n := range domainTables {
		if got[n] {
			t.Errorf("tabella identity.%s ancora presente dopo Down", n)
		}
	}

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("Up dopo Down: %v", err)
	}
}

func TestConstraints(t *testing.T) {
	pool, dsn := newPool(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("Up: %v", err)
	}
	const u1, u2, org, team, res = "00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-0000000000a1",
		"00000000-0000-0000-0000-0000000000b1",
		"00000000-0000-0000-0000-0000000000c1"
	hash := `decode(repeat('ab', 32), 'hex')`

	mustExec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("attesa riuscita: %s: %v", sql, err)
		}
	}
	mustFail := func(name, sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Errorf("%s: doveva violare un vincolo", name)
		}
	}

	mustExec(`INSERT INTO identity.users (id, username, email) VALUES ('` + u1 + `', 'alice', 'a@example.com')`)
	mustExec(`INSERT INTO identity.users (id, username) VALUES ('` + u2 + `', 'bob')`)
	mustFail("username duplicato", `INSERT INTO identity.users (id, username) VALUES (gen_random_uuid(), 'alice')`)
	mustFail("username maiuscolo", `INSERT INTO identity.users (id, username) VALUES (gen_random_uuid(), 'Carol')`)
	mustFail("email duplicata (case-insensitive)", `INSERT INTO identity.users (id, username, email) VALUES (gen_random_uuid(), 'dave', 'A@Example.com')`)

	mustExec(`INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes)
		VALUES (gen_random_uuid(), '` + u1 + `', 't1', ` + hash + `, 'wxyz', ARRAY['read:user'])`)
	mustFail("token_hash duplicato", `INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes)
		VALUES (gen_random_uuid(), '`+u1+`', 't2', `+hash+`, 'wxyz', ARRAY['read:user'])`)
	mustFail("scope sconosciuto", `INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes)
		VALUES (gen_random_uuid(), '`+u1+`', 't3', decode(repeat('cd', 32), 'hex'), 'wxyz', ARRAY['root'])`)
	mustFail("hash non di 32 byte (token in chiaro)", `INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes)
		VALUES (gen_random_uuid(), '`+u1+`', 't4', 'gst_plaintext'::bytea, 'wxyz', ARRAY['read:user'])`)

	const fp = "SHA256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	mustExec(`INSERT INTO identity.ssh_keys (id, user_id, title, key_type, public_key, fingerprint_sha256)
		VALUES (gen_random_uuid(), '` + u1 + `', 'k', 'ssh-ed25519', 'ssh-ed25519 AAAA', '` + fp + `')`)
	mustFail("fingerprint duplicato fra utenti", `INSERT INTO identity.ssh_keys (id, user_id, title, key_type, public_key, fingerprint_sha256)
		VALUES (gen_random_uuid(), '`+u2+`', 'k', 'ssh-ed25519', 'ssh-ed25519 AAAA', '`+fp+`')`)

	mustExec(`INSERT INTO identity.organizations (id, name) VALUES ('` + org + `', 'acme')`)
	mustExec(`INSERT INTO identity.teams (id, org_id, name) VALUES ('` + team + `', '` + org + `', 'core')`)
	mustFail("membro di team non membro dell'org",
		`INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ('`+team+`', '`+org+`', '`+u1+`')`)
	mustExec(`INSERT INTO identity.org_members (org_id, user_id, role) VALUES ('` + org + `', '` + u1 + `', 'owner')`)
	mustExec(`INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ('` + team + `', '` + org + `', '` + u1 + `')`)

	mustFail("grant senza soggetto", `INSERT INTO identity.resource_grants (id, resource_id, role) VALUES (gen_random_uuid(), '`+res+`', 'read')`)
	mustFail("grant con due soggetti", `INSERT INTO identity.resource_grants (id, resource_id, user_id, team_id, role)
		VALUES (gen_random_uuid(), '`+res+`', '`+u1+`', '`+team+`', 'read')`)
	mustExec(`INSERT INTO identity.resource_grants (id, resource_id, user_id, role) VALUES (gen_random_uuid(), '` + res + `', '` + u1 + `', 'read')`)
	mustFail("grant duplicato", `INSERT INTO identity.resource_grants (id, resource_id, user_id, role) VALUES (gen_random_uuid(), '`+res+`', '`+u1+`', 'write')`)

	// Uscire dall'org toglie dai team a cascata.
	mustExec(`DELETE FROM identity.org_members WHERE org_id = '` + org + `' AND user_id = '` + u1 + `'`)
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.team_members`).Scan(&n); err != nil || n != 0 {
		t.Errorf("team_members dopo uscita dall'org: n=%d err=%v", n, err)
	}
}
