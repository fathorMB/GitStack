//go:build integration

package backup

import (
	"bytes"
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

// seedExternal prepara come il DBA e l'installer un Postgres esterno non
// superuser con le migrazioni reali e due tabelle di prova; ritorna la
// password dell'utente di GitStack.
func seedExternal(t *testing.T, s *pgtest.Server) string {
	t.Helper()
	const gsPw, idPw = "gs-pw-ext", "id-pw-ext"
	s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", []string{"GITSTACK_DB_PASSWORD=" + gsPw},
		readRepo(t, "deploy", "postgres-prepare.sql"))
	s.SQL(t, "gitstack", gsPw, "gitstack", nil,
		readRepo(t, "deploy", "gitstack", "files", "identity-bootstrap-role.sql"), "identity_password="+idPw)
	applyMigrations(t, s, "identity_app", idPw, "identity", "services", "identity", "internal", "migrate", "sql")
	s.SQL(t, "gitstack", gsPw, "gitstack", nil, `CREATE SCHEMA IF NOT EXISTS core;`)
	applyMigrations(t, s, "gitstack", gsPw, "core", "services", "core", "internal", "migrate", "sql")
	s.SQL(t, "identity_app", idPw, "gitstack", []string{"PGOPTIONS=-c search_path=identity"},
		`CREATE TABLE gs_marker(v text); INSERT INTO gs_marker VALUES ('identity-uno');`)
	s.SQL(t, "gitstack", gsPw, "gitstack", []string{"PGOPTIONS=-c search_path=core"},
		`CREATE TABLE gs_marker(v text); INSERT INTO gs_marker VALUES ('core-uno');`)
	return gsPw
}

// N7 con N8: backup cifrato su MinIO da un Postgres esterno (pod client, non
// StatefulSet) e `restore --from s3` su un ambiente pulito, sullo stesso
// database esterno dopo che i dati sono cambiati.
func TestS3MinIO_BackupERestoreConPostgresEsterno(t *testing.T) {
	s := pgtest.Start(t, 15)
	s.WithClient(t, 15)
	gsPw := seedExternal(t, s)
	markers := func() string {
		return s.SQL(t, "gitstack", gsPw, "gitstack", nil, `
SELECT string_agg(v, ',' ORDER BY v) FROM (SELECT v FROM identity.gs_marker UNION ALL SELECT v FROM core.gs_marker) x`)
	}
	before := markers()

	ep := startMinio(t)
	const bucket = "gitstack-backups-ext"
	makeBucket(t, ep, bucket)
	s3c := config.S3{Endpoint: "http://" + ep, Bucket: bucket, Prefix: "ext", AccessKey: minioAccess,
		SecretKey: minioSecret, KeyFile: "/etc/gitstack/backup.key", Retention: "2"}
	if err := s3c.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := NewS3Store(s3c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := []byte("una-chiave-di-prova-lunga-abbastanza")
	pg := config.Postgres{External: true, Host: "127.0.0.1", Port: s.Port, Database: "gitstack", User: "gitstack"}

	e := newEnv(t)
	e.o.Key = key
	e.o.Cfg.Backup.S3 = &s3c
	e.o.Cfg.Postgres = pg
	e.o.Cluster = &pgCluster{fakeCluster: e.f, srv: s, env: pgtest.ClientEnv("gitstack", gsPw, "gitstack")}
	res, err := Backup(ctx, e.o)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, e.log)
	}
	if _, err := Upload(ctx, e.o, st, res.Path); err != nil {
		t.Fatalf("upload: %v", err)
	}
	raw := rawObject(t, ep, bucket, "ext/"+filepath.Base(res.Path))
	if !bytes.HasPrefix(raw, encMagic) || bytes.Contains(raw, []byte("identity-uno")) {
		t.Error("l'archivio su S3 deve essere cifrato, senza dati in chiaro")
	}

	s.SQL(t, "gitstack", gsPw, "gitstack", nil, `
DELETE FROM core.gs_marker; INSERT INTO core.gs_marker VALUES ('core-dopo'); DROP TABLE identity.gs_marker;`)

	clean := newEnv(t)
	clean.o.Key = key
	clean.o.Cfg.Backup.S3 = &s3c
	clean.o.Cfg.Postgres = pg
	clean.o.Cluster = &pgCluster{fakeCluster: clean.f, srv: s, env: pgtest.ClientEnv("gitstack", gsPw, "gitstack")}
	p, err := FetchFromS3(ctx, clean.o, st, "", clean.dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, clean.o, p); err != nil {
		t.Fatalf("restore da S3: %v\n%s", err, clean.log)
	}
	if got := markers(); got != before {
		t.Errorf("dati dopo il restore da S3: %q, attesi %q", got, before)
	}
}
