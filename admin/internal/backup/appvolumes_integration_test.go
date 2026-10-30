//go:build integration

package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/pgtest"
)

// TestAppVolumesCmd_SuPostgresReale esegue appVolumesCmd (la stessa costante del backup, via sh -c nel
// container client come PGExec) su un Postgres vero con le migrazioni di core (GIT-357, H3, D19): di
// default i volumi sono inclusi, quelli con include_in_backup = false restano fuori.
func TestAppVolumesCmd_SuPostgresReale(t *testing.T) {
	s := pgtest.Start(t, 15)
	s.WithClient(t, 15)
	gsPw := seedExternal(t, s)

	const repoID = "11111111-2222-4333-8444-555555555555"
	sql := `
INSERT INTO core.resources (id, type, name) VALUES ('` + repoID + `', 'repo', 'alice/wiki');
INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, owner_name)
  VALUES ('` + repoID + `', 'user', '99999999-2222-4333-8444-555555555555', 'wiki', 'alice');
INSERT INTO core.apps (id, repo_id, name, active_version, next_version, guaranteed_cpu_millis, guaranteed_memory_bytes,
                       limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', '` + repoID + `', 'wiki', 1, 2, 100, 1000, 100, 1000, now(), now()),
  ('aaaaaaaa-0000-4000-8000-000000000002', '` + repoID + `', 'cms', 1, 2, 100, 1000, 100, 1000, now(), now());
INSERT INTO core.app_volumes (app_id, name, mount_path, size_bytes, version, created_at) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'data', '/data', 1073741824, 2, now()),
  ('aaaaaaaa-0000-4000-8000-000000000001', 'uploads', '/uploads', 2097152, 3, now()),
  ('aaaaaaaa-0000-4000-8000-000000000002', 'db', '/var/lib/postgresql/data', 5242880, 2, now());
UPDATE core.app_volumes SET include_in_backup = false WHERE name = 'uploads';
`
	s.SQL(t, "gitstack", gsPw, "gitstack", nil, sql)

	var out bytes.Buffer
	if err := s.Exec(context.Background(), pgtest.ClientEnv("gitstack", gsPw, "gitstack"), nil, &out, "sh", "-c", appVolumesCmd); err != nil {
		t.Fatalf("appVolumesCmd: %v", err)
	}
	prefix := "|" + repoID + "|alice|wiki|"
	want := []string{
		"aaaaaaaa-0000-4000-8000-000000000001" + prefix + "wiki|data|1073741824",
		"aaaaaaaa-0000-4000-8000-000000000002" + prefix + "cms|db|5242880",
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("righe di appVolumesCmd:\n%s\nattese:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range got {
		if _, err := parseAppVolumeRow(line); err != nil {
			t.Errorf("parseAppVolumeRow(%q): %v", line, err)
		}
	}
}

// TestS3MinIO_BackupERestoreDeiVolumiApp: il backup cifrato con i volumi delle app va su un MinIO vero
// e un restore su un ambiente pulito, da S3, rimette i dati scritti prima del backup (GIT-357, N7).
func TestS3MinIO_BackupERestoreDeiVolumiApp(t *testing.T) {
	ep := startMinio(t)
	const bucket = "gitstack-backups-vol"
	makeBucket(t, ep, bucket)
	s3c := config.S3{Endpoint: "http://" + ep, Bucket: bucket, Prefix: "prod", AccessKey: minioAccess,
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

	e, _, _ := newAppVolEnv(t)
	e.o.Key = key
	e.o.Cfg.Backup.S3 = &s3c
	e.o.Now = func() time.Time { return time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC) }
	e.f.appVolumes = avRow(avApp, "data", 4096)
	res, err := Backup(ctx, e.o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(ctx, e.o, st, res.Path); err != nil {
		t.Fatal(err)
	}
	raw := rawObject(t, ep, bucket, "prod/"+filepath.Base(res.Path))
	if bytes.Contains(raw, []byte("DATI-SCRITTI-PRIMA-DEL-BACKUP")) || bytes.Contains(raw, []byte("app-volumes/")) {
		t.Error("i dati del volume sono in chiaro su S3")
	}

	clean, cc, _ := newAppVolEnv(t)
	clean.o.Key = key
	clean.o.Cfg.Backup.S3 = &s3c
	dst := filepath.Join(t.TempDir(), "pvc-restore")
	if err := os.MkdirAll(dst, 0o755); err != nil { // EnsureAppVolume la crea
		t.Fatal(err)
	}
	cc.ensured["gitstack-app-"+avApp+"/vol-data"] = dst
	p, err := FetchFromS3(ctx, clean.o, st, "", clean.dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, clean.o, p); err != nil {
		t.Fatalf("restore da S3: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "wiki", "pagina.md"))
	if err != nil || string(b) != "DATI-SCRITTI-PRIMA-DEL-BACKUP" {
		t.Errorf("dato ripristinato da S3 = %q, %v", b, err)
	}
}
