package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

func TestBackupConS3SenzaChiaveRifiutato(t *testing.T) {
	e := newEnv(t)
	e.o.Cfg.Backup.S3 = &config.S3{Endpoint: "e", Bucket: "b", AccessKey: "AKIA1234", SecretKey: "SEGRETO-1234", KeyFile: "/k"}
	var ref *RefusedError
	if _, err := Backup(context.Background(), e.o); !errors.As(err, &ref) {
		t.Fatalf("atteso rifiuto: %v", err)
	}
	if ents, _ := os.ReadDir(e.dest); len(ents) != 0 {
		t.Errorf("creato qualcosa nonostante il rifiuto: %v", ents)
	}
	if len(e.f.calls) != 0 {
		t.Errorf("il cluster è stato toccato: %v", e.f.calls)
	}
}

func TestUploadRifiutaArchivioNonCifrato(t *testing.T) {
	e := newEnv(t)
	e.o.Cfg.Backup.S3 = &config.S3{Endpoint: "e", Bucket: "b", AccessKey: "AKIA1234", SecretKey: "SEGRETO-1234", KeyFile: "/k"}
	res, err := Backup(context.Background(), e.o) // senza chiave: la guardia è in cli, qui si salta
	_ = res
	if err == nil {
		// Backup rifiuta già; verifico Upload con un file in chiaro scritto a mano.
		t.Fatal("atteso rifiuto di Backup")
	}
	plain := filepath.Join(t.TempDir(), "gitstack-backup-20260101T000000Z-x.tar.gz.enc")
	if err := os.WriteFile(plain, []byte("non cifrato"), 0o600); err != nil {
		t.Fatal(err)
	}
	var ref *RefusedError
	if _, err := Upload(context.Background(), e.o, nil, plain); !errors.As(err, &ref) {
		t.Errorf("file senza magic caricato: %v", err)
	}
}

func TestIsArchiveName(t *testing.T) {
	for n, want := range map[string]bool{
		"gitstack-backup-20260101T000000Z-sha-abc.tar.gz.enc": true,
		"gitstack-backup-20260101T000000Z-sha-abc.tar.gz":     true,
		"gitstack-backup-x.tar.gz.sha256":                     false,
		"../gitstack-backup-x.tar.gz":                         false,
		"a/gitstack-backup-x.tar.gz":                          false,
		"altro.tar.gz":                                        false,
	} {
		if got := IsArchiveName(n); got != want {
			t.Errorf("%q: %v", n, got)
		}
	}
}

func TestScrubberToglieLeChiavi(t *testing.T) {
	sc := newScrubber(config.S3{AccessKey: "AKIA-ACCESS-1", SecretKey: "SEGRETO-LUNGO-99"})
	err := sc.err("put", errors.New("denied AKIA-ACCESS-1 / SEGRETO-LUNGO-99"))
	if strings.Contains(err.Error(), "AKIA-ACCESS-1") || strings.Contains(err.Error(), "SEGRETO-LUNGO-99") {
		t.Errorf("chiavi nell'errore: %v", err)
	}
}
