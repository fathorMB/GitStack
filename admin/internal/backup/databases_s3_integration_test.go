//go:build integration

package backup

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// DB14: i dump dei database seguono la copia S3 senza codice dedicato. L'oggetto su S3 e' cifrato (nessun
// pezzo del dump in chiaro) e un restore da S3 su un ambiente pulito rimette i database. Gira contro un MinIO
// vero come gli altri test S3 (vedi s3_integration_test.go).
func TestS3MinIO_DatabaseNelBackupENelRestoreDaS3(t *testing.T) {
	ep := startMinio(t)
	const bucket = "gitstack-backups-db"
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

	e, _ := newDBEnv(t)
	e.o.Key = key
	e.o.Cfg.Backup.S3 = &s3c
	res, err := Backup(ctx, e.o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(ctx, e.o, st, res.Path); err != nil {
		t.Fatal(err)
	}
	arcs, err := S3Archives(ctx, st)
	if err != nil || len(arcs) != 1 {
		t.Fatalf("archivi su S3: %+v %v", arcs, err)
	}
	raw := rawObject(t, ep, bucket, "prod/"+arcs[0].Name)
	if !bytes.HasPrefix(raw, encMagic) {
		t.Error("l'oggetto su S3 non e' cifrato")
	}
	for _, plain := range []string{"PGDMP", "dati di shop", "dati di erp", "databases/"} {
		if bytes.Contains(raw, []byte(plain)) {
			t.Errorf("%q in chiaro su S3", plain)
		}
	}

	clean, cc := newDBEnv(t)
	clean.o.Key = key
	clean.o.Cfg.Backup.S3 = &s3c
	clean.o.DatabaseReadyTimeout, clean.o.DatabaseReadyPoll = 5*time.Second, time.Millisecond
	p, err := FetchFromS3(ctx, clean.o, st, "", filepath.Join(t.TempDir(), "dest"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, clean.o, p); err != nil {
		t.Fatalf("restore da S3: %v", err)
	}
	if cc.restored[dbShopID] != "PGDMP dati di shop" || cc.restored[dbErpID] != "PGDMP dati di erp" {
		t.Fatalf("database ripristinati da S3: %v", cc.restored)
	}
}
