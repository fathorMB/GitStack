//go:build integration

package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigration0036_App (GIT-342, H5/H8): app, versioni e deploy; unicità del nome nel repo e
// dell'indirizzo, versione unica per app, cascata dal repo (purge dopo 7 giorni, R2) e dall'app;
// il down toglie le quattro tabelle e il nuovo up rifunziona.
func TestMigration0036_App(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	repo, repo2, owner := uuid.New(), uuid.New(), uuid.New()
	for i, r := range []uuid.UUID{repo, repo2} {
		name := []string{"r", "s"}[i]
		if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, r, "a/"+name); err != nil {
			t.Fatal(err)
		}
		if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, $3)`, r, owner, name); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	insertApp := func(id, repoID uuid.UUID, name string, label any) error {
		return exec(`INSERT INTO core.apps (id, repo_id, name, address_label, guaranteed_cpu_millis, guaranteed_memory_bytes,
			limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, $3, $4, 100, 1000, 200, 2000, $5, $5)`,
			id, repoID, name, label, now)
	}
	insertVersion := func(app uuid.UUID, n int) error {
		return exec(`INSERT INTO core.app_versions (app_id, number, trigger, image_reference, image_source, config, created_at)
			VALUES ($1, $2, 'create', 'h/a/r:t', 'registry', '{}'::jsonb, $3)`, app, n, now)
	}
	app1, app2 := uuid.New(), uuid.New()
	if err := insertApp(app1, repo, "web", "web-r-a"); err != nil {
		t.Fatal(err)
	}
	// Default: stato starting, accesso login (H6), versione attiva 1.
	var status, access string
	var active int
	if err := pool.QueryRow(ctx, `SELECT status, access_mode, active_version FROM core.apps WHERE id = $1`, app1).Scan(&status, &access, &active); err != nil ||
		status != "starting" || access != "login" || active != 1 {
		t.Fatalf("default: %s %s %d %v", status, access, active, err)
	}
	// Nome unico nel repo, uguale in un altro repo; indirizzo unico in tutta l'istanza, NULL ripetibile.
	if insertApp(uuid.New(), repo, "web", nil) == nil {
		t.Error("il nome è unico nel repo")
	}
	if insertApp(uuid.New(), repo2, "web", "web-r-a") == nil {
		t.Error("l'indirizzo è unico")
	}
	if err := insertApp(app2, repo2, "web", nil); err != nil {
		t.Fatalf("stesso nome in un altro repo: %v", err)
	}
	if err := insertApp(uuid.New(), repo2, "worker", nil); err != nil {
		t.Fatalf("due app senza indirizzo: %v", err)
	}
	// Nome non valido, stato non valido.
	if insertApp(uuid.New(), repo, "Web", nil) == nil || insertApp(uuid.New(), repo, "-web", nil) == nil {
		t.Error("il nome è minuscolo, senza trattino ai bordi")
	}
	if exec(`UPDATE core.apps SET status = 'boh' WHERE id = $1`, app1) == nil {
		t.Error("stato non valido accettato")
	}
	// Il tetto non sta sotto il garantito.
	if exec(`UPDATE core.apps SET limit_cpu_millis = 50 WHERE id = $1`, app1) == nil {
		t.Error("tetto sotto il garantito accettato")
	}

	// Versioni: (app, numero) unico; deploy legati a una versione che esiste.
	if err := insertVersion(app1, 1); err != nil {
		t.Fatal(err)
	}
	if insertVersion(app1, 1) == nil {
		t.Error("il numero di versione è unico per app")
	}
	if err := insertVersion(app1, 2); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, 2, $3)`, uuid.New(), app1, now); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, 7, $3)`, uuid.New(), app1, now) == nil {
		t.Error("deploy di una versione che non c'è")
	}
	if exec(`INSERT INTO core.app_versions (app_id, number, trigger, image_reference, image_source, config, created_at)
		VALUES ($1, 3, 'boh', 'x', 'registry', '{}'::jsonb, $2)`, app1, now) == nil {
		t.Error("trigger non valido accettato")
	}
	// Quote per owner: una riga per owner, nome in minuscolo.
	if err := exec(`INSERT INTO core.app_owner_quotas (owner_name, cpu_millis, memory_bytes, disk_bytes) VALUES ('alice', 1, 2, 3)`); err != nil {
		t.Fatal(err)
	}
	if exec(`INSERT INTO core.app_owner_quotas (owner_name, cpu_millis, memory_bytes, disk_bytes) VALUES ('Bob', 1, 2, 3)`) == nil {
		t.Error("il nome dell'owner è in minuscolo")
	}

	// Cascate: eliminare l'app toglie versioni e deploy; eliminare il repo (purge) toglie le sue app.
	if err := exec(`DELETE FROM core.apps WHERE id = $1`, app1); err != nil {
		t.Fatal(err)
	}
	if count("app_versions") != 0 || count("app_deploys") != 0 {
		t.Fatalf("dopo l'eliminazione dell'app: %d versioni, %d deploy", count("app_versions"), count("app_deploys"))
	}
	if err := exec(`DELETE FROM core.resources WHERE id = $1`, repo2); err != nil {
		t.Fatal(err)
	}
	if n := count("apps"); n != 0 {
		t.Fatalf("app dopo l'eliminazione del repo: %d", n)
	}

	if err := migrate.Down(ctx, pool, dsn, 14); err != nil {
		t.Fatalf("down 0038: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
		AND table_name IN ('apps', 'app_versions', 'app_deploys', 'app_owner_quotas', 'app_capacity_settings')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if count("apps") != 0 {
		t.Error("tabella apps non vuota dopo il nuovo up")
	}
}
