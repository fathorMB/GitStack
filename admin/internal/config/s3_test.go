package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Il blocco che scrive deploy/install.sh (apici singoli, ” per l'apice).
const installerS3 = `version: 1
host: h
backup:
  destination: /var/backups/gitstack
  retention: 7
  s3:
    endpoint: 's3.x'
    bucket: 'b'
    access_key: 'AKIA1234'
    secret_key: 'se''greto'
    key_file: '/etc/gitstack/backup.key'
    retention: 'bucket'
`

func TestS3_BloccoDellInstaller(t *testing.T) {
	c, err := Parse([]byte(installerS3), "x")
	if err != nil {
		t.Fatal(err)
	}
	s := c.Backup.S3
	if s == nil || s.SecretKey != "se'greto" || s.KeepCount() != 0 || s.Endpoint != "s3.x" {
		t.Errorf("S3: %+v", s)
	}
	if c.Backup.Retention != 7 {
		t.Errorf("retention locale: %d", c.Backup.Retention)
	}
}

func TestS3_ConservazioneDefaultENumero(t *testing.T) {
	s := S3{}
	if s.KeepCount() != DefaultS3Retention {
		t.Errorf("default: %d", s.KeepCount())
	}
	s.Retention = "12"
	if s.KeepCount() != 12 {
		t.Errorf("12: %d", s.KeepCount())
	}
	s.Retention = "Bucket"
	if s.KeepCount() != 0 {
		t.Errorf("bucket: %d", s.KeepCount())
	}
}

func TestS3_ValidateRifiuti(t *testing.T) {
	ok := S3{Endpoint: "e", Bucket: "b", AccessKey: "a", SecretKey: "s", KeyFile: "/k"}
	cases := map[string]func(*S3){
		"key_file": func(s *S3) { s.KeyFile = "" },
		"endpoint": func(s *S3) { s.Endpoint = "" },
		"bucket":   func(s *S3) { s.Bucket = "" },
		"access":   func(s *S3) { s.AccessKey = "" },
		"secret":   func(s *S3) { s.SecretKey = "" },
		"retention": func(s *S3) {
			s.Retention = "0"
		},
		"assoluto": func(s *S3) { s.KeyFile = "chiave" },
	}
	for name, mut := range cases {
		s := ok
		mut(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accettato", name)
		}
	}
	s := ok
	if err := s.Validate(); err != nil {
		t.Errorf("valida rifiutata: %v", err)
	}
}

func TestSetBackupS3_ConservaIlRestoEScrive0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# commento dell'installer\nversion: 1\nhost: gs.local\nimage_tag: sha-abc\nbackup:\n  destination: /var/backups/gitstack\n  retention: 7\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &S3{Endpoint: "s3.example.test", Bucket: "b", AccessKey: "AK", SecretKey: "SK", KeyFile: "/etc/gitstack/k"}
	if err := SetBackupS3(p, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "# commento dell'installer") {
		t.Errorf("commento perso:\n%s", b)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "gs.local" || c.ImageTag != "sha-abc" || c.Backup.Retention != 7 || c.Backup.S3 == nil || c.Backup.S3.Bucket != "b" {
		t.Errorf("config: %+v", c)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("modo %v", fi.Mode().Perm())
		}
	}
	// Riscrittura: sostituisce, non duplica; poi nil la toglie.
	s.Bucket = "altro"
	if err := SetBackupS3(p, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Count(string(b), "s3:") != 1 {
		t.Errorf("sezione duplicata:\n%s", b)
	}
	if err := SetBackupS3(p, nil); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil || c.Backup.S3 != nil {
		t.Errorf("rimozione: %v %+v", err, c)
	}
}
