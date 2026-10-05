//go:build integration

package migrate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newResource(t *testing.T, pool *pgxpool.Pool, typ, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO core.resources (id, type, name) VALUES ($1, $2, $3)`, id, typ, name); err != nil {
		t.Fatalf("insert resource %s/%s: %v", typ, name, err)
	}
	return id
}

func insertRepo(pool *pgxpool.Pool, resID, ownerID uuid.UUID, name string) error {
	_, err := pool.Exec(context.Background(),
		`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name)
		 VALUES ($1, 'user', $2, $3)`, resID, ownerID, name)
	return err
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("atteso errore Postgres %s, ottenuto %v", code, err)
	}
	if pgErr.Code != code {
		t.Fatalf("atteso codice %s, ottenuto %s (%s)", code, pgErr.Code, pgErr.Message)
	}
}

func TestRepositories_Vincoli(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	// Stesso nome sotto owner diversi: ok. Le risorse 'repo' possono avere
	// lo stesso (type, name).
	if err := insertRepo(pool, newResource(t, pool, "repo", "alice/app"), alice, "app"); err != nil {
		t.Fatal(err)
	}
	if err := insertRepo(pool, newResource(t, pool, "repo", "bob/app"), bob, "app"); err != nil {
		t.Fatalf("stesso nome con owner diverso deve essere ammesso: %v", err)
	}
	// Anche due risorse repo con lo stesso name generico non confliggono.
	newResource(t, pool, "repo", "app")
	newResource(t, pool, "repo", "app")

	// Stesso nome sotto lo stesso owner: violazione, anche con deleted_at.
	err := insertRepo(pool, newResource(t, pool, "repo", "alice/app2"), alice, "app")
	wantCode(t, err, "23505")
	if _, err := pool.Exec(ctx, `UPDATE core.repositories SET deleted_at = now()
		WHERE owner_id = $1 AND name = 'app'`, alice); err != nil {
		t.Fatal(err)
	}
	err = insertRepo(pool, newResource(t, pool, "repo", "alice/app3"), alice, "app")
	wantCode(t, err, "23505")

	// R11: maiuscole, '.git', iniziale '.', lunghezza.
	for _, bad := range []string{"Repo", "x.git", ".hidden", "", "a b", repeat("a", 101)} {
		err := insertRepo(pool, newResource(t, pool, "repo", "bad/"+uuid.NewString()), alice, bad)
		wantCode(t, err, "23514")
	}
	for _, ok := range []string{"_x", "-x", "a.b_c-d", repeat("a", 100), "x.github"} {
		if err := insertRepo(pool, newResource(t, pool, "repo", "ok/"+uuid.NewString()), alice, ok); err != nil {
			t.Fatalf("nome %q dovrebbe essere ammesso: %v", ok, err)
		}
	}

	// owner_type e visibility.
	_, err = pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name)
		VALUES ($1, 'team', $2, 'z')`, newResource(t, pool, "repo", "t/"+uuid.NewString()), alice)
	wantCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, visibility)
		VALUES ($1, 'user', $2, 'pub', 'public')`, newResource(t, pool, "repo", "t/"+uuid.NewString()), alice)
	wantCode(t, err, "23514")

	// Default (P7, R4, R9).
	var vis, branch string
	var protect bool
	if err := pool.QueryRow(ctx, `SELECT visibility, default_branch, protect_default_branch
		FROM core.repositories WHERE owner_id = $1 AND name = 'app'`, bob).Scan(&vis, &branch, &protect); err != nil {
		t.Fatal(err)
	}
	if vis != "private" || branch != "main" || !protect {
		t.Fatalf("default inattesi: %s %s %v", vis, branch, protect)
	}

	// Risorse non-repo: (type, name) resta unico.
	newResource(t, pool, "app", "web")
	_, err = pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'app', 'web')`, uuid.New())
	wantCode(t, err, "23505")
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestPullRequests_Vincoli(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repoRes := newResource(t, pool, "repo", "alice/app")
	if err := insertRepo(pool, repoRes, uuid.New(), "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, repoRes); err != nil {
		t.Fatal(err)
	}
	ins := func(num int, src, dst, state string) error {
		_, err := pool.Exec(ctx, `INSERT INTO core.pull_requests
			(id, repo_id, number, source_branch, target_branch, title, state, author_id)
			VALUES ($1, $2, $3, $4, $5, 't', $6, $7)`,
			uuid.New(), repoRes, num, src, dst, state, uuid.New())
		return err
	}
	if err := ins(1, "feat", "main", "open"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, ins(1, "feat2", "main", "open"), "23505")
	wantCode(t, ins(2, "main", "main", "open"), "23514")
	wantCode(t, ins(3, "feat", "main", "draft"), "23514")

	// ON DELETE CASCADE dalla risorsa fino alle PR.
	if _, err := pool.Exec(ctx, `DELETE FROM core.resources WHERE id = $1`, repoRes); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_requests`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("PR attese a cascata: n=%d err=%v", n, err)
	}
}

func TestMigration0002_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()

	if err := migrate.Down(ctx, pool, dsn, 2); err != nil {
		t.Fatalf("down 0002: %v", err)
	}
	for _, tbl := range []string{"repositories", "repo_counters", "pull_requests"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
			WHERE table_schema='core' AND table_name=$1)`, tbl).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("%s non dovrebbe esistere dopo il down", tbl)
		}
	}
	// Il vincolo originale e' tornato.
	newResource(t, pool, "repo", "x")
	_, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'x')`, uuid.New())
	wantCode(t, err, "23505")

	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0002 dopo down: %v", err)
	}
}

func TestMigration0003_OwnerNameSaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	hasCol := func() bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema='core' AND table_name='repositories' AND column_name='owner_name')`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !hasCol() {
		t.Fatal("owner_name dovrebbe esistere dopo le migrazioni")
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0003: %v", err)
	}
	if hasCol() {
		t.Fatal("owner_name non dovrebbe esistere dopo il down")
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up 0003 dopo down: %v", err)
	}
	if !hasCol() {
		t.Fatal("owner_name dovrebbe tornare dopo up")
	}
}
