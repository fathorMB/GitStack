//go:build integration

package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/pgtest"
)

// pgCluster è il fakeCluster con i comandi di database eseguiti davvero su
// un Postgres esterno: la stessa forma di `kubectl exec` nel pod client, con
// l'utente non superuser preparato dal DBA.
type pgCluster struct {
	*fakeCluster
	srv *pgtest.Server
	env []string
}

func (c *pgCluster) PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	return c.srv.Exec(ctx, c.env, stdin, stdout, args...)
}

func repoFile(t *testing.T, rel ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(append([]string{filepath.Dir(file), "..", "..", ".."}, rel...)...)
	return p
}

func readRepo(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(repoFile(t, rel...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// applyMigrations applica i file *.up.sql di dir, in ordine, come user e con
// il search_path dello schema (come golang-migrate nei servizi).
func applyMigrations(t *testing.T, s *pgtest.Server, user, pw, schema string, dir ...string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoFile(t, dir...), "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("nessuna migrazione in %v: %v", dir, err)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s.SQL(t, user, pw, "gitstack", []string{"PGOPTIONS=-c search_path=" + schema}, string(b))
	}
	return len(files)
}

func TestBackupRestoreSuPostgresEsterno(t *testing.T) {
	for _, major := range []int{15, 17} {
		t.Run(fmt.Sprintf("pg%d", major), func(t *testing.T) {
			s := pgtest.Start(t, major)
			// Client come nel chart: stessa versione principale del server.
			s.WithClient(t, major)
			const gsPw, idPw = "gs-pw-ext", "id-pw-ext"

			// 1) Il DBA prepara database e utente (non superuser).
			s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", []string{"GITSTACK_DB_PASSWORD=" + gsPw},
				readRepo(t, "deploy", "postgres-prepare.sql"))
			if su := s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", nil,
				`SELECT rolsuper FROM pg_roles WHERE rolname = 'gitstack'`); su != "f" {
				t.Fatalf("l'utente di GitStack non deve essere superuser (rolsuper=%q)", su)
			}

			// 2) Installazione: ruolo e schema di identity (initContainer del
			// chart), poi le migrazioni reali dei due servizi.
			s.SQL(t, "gitstack", gsPw, "gitstack", nil,
				readRepo(t, "deploy", "gitstack", "files", "identity-bootstrap-role.sql"), "identity_password="+idPw)
			nid := applyMigrations(t, s, "identity_app", idPw, "identity", "services", "identity", "internal", "migrate", "sql")
			s.SQL(t, "gitstack", gsPw, "gitstack", nil, `CREATE SCHEMA IF NOT EXISTS core;`)
			ncore := applyMigrations(t, s, "gitstack", gsPw, "core", "services", "core", "internal", "migrate", "sql")
			t.Logf("migrazioni applicate: identity %d, core %d", nid, ncore)

			s.SQL(t, "identity_app", idPw, "gitstack", []string{"PGOPTIONS=-c search_path=identity"},
				`CREATE TABLE gs_marker(v text); INSERT INTO gs_marker VALUES ('identity-uno'), ('identity-due');`)
			s.SQL(t, "gitstack", gsPw, "gitstack", []string{"PGOPTIONS=-c search_path=core"},
				`CREATE TABLE gs_marker(v text); INSERT INTO gs_marker VALUES ('core-uno');`)

			inventory := func() string {
				return s.SQL(t, "gitstack", gsPw, "gitstack", nil, `
SELECT schemaname || '.' || tablename || ':' || tableowner FROM pg_tables
WHERE schemaname IN ('identity', 'core') ORDER BY 1`)
			}
			markers := func() string {
				return s.SQL(t, "gitstack", gsPw, "gitstack", nil, `
SELECT string_agg(v, ',' ORDER BY v) FROM (SELECT v FROM identity.gs_marker UNION ALL SELECT v FROM core.gs_marker) x`)
			}
			before, markersBefore := inventory(), markers()
			if !strings.Contains(before, "identity.gs_marker:identity_app") || !strings.Contains(before, "core.gs_marker:gitstack") {
				t.Fatalf("inventario inatteso:\n%s", before)
			}

			// 3) Backup con la stessa procedura dell'installazione interna.
			e := newEnv(t)
			e.o.Cfg.Postgres = config.Postgres{External: true, Host: "127.0.0.1", Port: s.Port, Database: "gitstack", User: "gitstack",
				PasswordSecret: "gs-postgres-external"}
			e.o.Cluster = &pgCluster{fakeCluster: e.f, srv: s, env: pgtest.ClientEnv("gitstack", gsPw, "gitstack")}
			res, err := Backup(context.Background(), e.o)
			if err != nil {
				t.Fatalf("backup: %v\n%s", err, e.log)
			}
			if !strings.Contains(e.log.String(), "dump di Postgres") {
				t.Errorf("log senza il dump: %s", e.log)
			}

			// 4) Il database cambia dopo il backup.
			s.SQL(t, "gitstack", gsPw, "gitstack", nil, `
DELETE FROM core.gs_marker; INSERT INTO core.gs_marker VALUES ('core-dopo');
DROP TABLE identity.gs_marker;`)

			// 5) Restore degli schemi dal backup.
			e.f.applied = nil
			if err := Restore(context.Background(), e.o, res.Path); err != nil {
				t.Fatalf("restore: %v\n%s", err, e.log)
			}
			if after := inventory(); after != before {
				t.Errorf("inventario dopo il restore diverso.\nprima:\n%s\ndopo:\n%s", before, after)
			}
			if got := markers(); got != markersBefore {
				t.Errorf("dati dopo il restore: %q, attesi %q", got, markersBefore)
			}
			owner := s.SQL(t, "gitstack", gsPw, "gitstack", nil, `SELECT nspowner::regrole FROM pg_namespace WHERE nspname = 'identity'`)
			if owner != "identity_app" {
				t.Errorf("proprietario dello schema identity: %q", owner)
			}
			for _, name := range e.f.applied {
				if name == "gs-postgres-external" {
					t.Errorf("il Secret del database esterno non va ripristinato")
				}
			}
			// 6) Il restore è ripetibile.
			if err := Restore(context.Background(), e.o, res.Path); err != nil {
				t.Fatalf("secondo restore: %v", err)
			}
			if got := markers(); got != markersBefore {
				t.Errorf("dati dopo il secondo restore: %q", got)
			}
		})
	}
}
