//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigrate0029_SegretiVariabiliEPermessiDelToken: segreti e variabili di repo e di
// organizzazione (nome univoco per bersaglio, bersaglio coerente con lo scope), permessi del
// token del job con default read; il down toglie tutto e il nuovo up rifunziona.
func TestMigrate0029_SegretiVariabiliEPermessiDelToken(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	repo, org, user := uuid.New(), uuid.New(), uuid.New()
	must(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo)
	must(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, user)

	secret := `INSERT INTO core.ci_secrets (id, scope, repo_id, org_id, name, ciphertext, nonce, secret_key_id, created_by)
		VALUES ($1, $2, $3, $4, $5, '\x01', decode(repeat('00', 12), 'hex'), 'k1', $6)`
	must(secret, uuid.New(), "repo", repo, nil, "TOKEN", user)
	must(secret, uuid.New(), "org", nil, org, "TOKEN", user)
	if exec(secret, uuid.New(), "repo", repo, nil, "TOKEN", user) == nil {
		t.Error("il nome di un segreto è univoco per repo")
	}
	if exec(secret, uuid.New(), "org", repo, org, "ALTRO", user) == nil {
		t.Error("scope e bersaglio devono coincidere")
	}
	if exec(secret, uuid.New(), "repo", repo, nil, "minuscolo", user) == nil {
		t.Error("il nome è in maiuscolo")
	}
	variable := `INSERT INTO core.ci_variables (id, scope, repo_id, org_id, name, value, created_by) VALUES ($1, $2, $3, $4, $5, 'v', $6)`
	must(variable, uuid.New(), "repo", repo, nil, "REGION", user)
	if exec(variable, uuid.New(), "repo", repo, nil, "REGION", user) == nil {
		t.Error("il nome di una variabile è univoco per repo")
	}
	// Permessi di default del token del job: read.
	must(`INSERT INTO core.ci_repo_state (repo_id) VALUES ($1)`, repo)
	var c, p, a string
	if err := pool.QueryRow(ctx, `SELECT token_contents, token_packages, token_actions FROM core.ci_repo_state WHERE repo_id = $1`, repo).Scan(&c, &p, &a); err != nil || c != "read" || p != "read" || a != "read" {
		t.Fatalf("default: %s %s %s %v", c, p, a, err)
	}
	if exec(`UPDATE core.ci_repo_state SET token_contents = 'admin' WHERE repo_id = $1`, repo) == nil {
		t.Error("none, read o write soltanto")
	}
	// Eliminando il repo spariscono i suoi segreti e le sue variabili.
	must(`DELETE FROM core.repositories WHERE resource_id = $1`, repo)
	var left int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.ci_secrets WHERE repo_id IS NOT NULL) + (SELECT count(*) FROM core.ci_variables)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("righe rimaste: %d %v", left, err)
	}

	if err := migrate.Down(ctx, pool, dsn, 26); err != nil {
		t.Fatalf("down 0029: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('ci_secrets', 'ci_variables')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'ci_repo_state' AND column_name LIKE 'token_%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("colonne dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
