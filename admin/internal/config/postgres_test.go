package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_PostgresEsterno(t *testing.T) {
	c, err := Parse([]byte(`version: 1
host: gs.example.com
postgres:
  external: true
  host: db.example.com
  database: gitstack
  user: gitstack
  ssl_mode: verify-full
  version: "16.4"
  password_secret: gitstack-postgres-external
  ca_secret: gitstack-postgres-ca
`), "t")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Postgres
	if !p.External || p.Host != "db.example.com" || p.Port != 5432 || p.Database != "gitstack" || p.SSLMode != "verify-full" || p.Version != "16.4" {
		t.Errorf("postgres: %+v", p)
	}
}

func TestParse_PostgresBundleDefault(t *testing.T) {
	c, err := Parse([]byte("version: 1\nhost: gs.example.com\n"), "t")
	if err != nil {
		t.Fatal(err)
	}
	if c.Postgres.External || c.Postgres.Port != 0 {
		t.Errorf("postgres: %+v", c.Postgres)
	}
}

func TestParse_PostgresEsternoSenzaHostERifiutato(t *testing.T) {
	_, err := Parse([]byte("version: 1\nhost: gs.example.com\npostgres:\n  external: true\n  database: gitstack\n"), "t")
	if err == nil || !strings.Contains(err.Error(), "postgres.host") {
		t.Errorf("errore: %v", err)
	}
}

// `gitstack config set backup.s3` riscrive il file: la sezione postgres
// (N8) deve restare, e s3 deve stare sotto backup.
func TestSetBackupS3_ConservaLaSezionePostgres(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	orig := "version: 1\nhost: gs.local\nbackup:\n  destination: /var/backups/gitstack\n  retention: 7\n" +
		"postgres:\n  external: true\n  host: db.example.com\n  database: gitstack\n  user: gitstack\n  version: \"16.4\"\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &S3{Endpoint: "s3.example.test", Bucket: "b", AccessKey: "AK", SecretKey: "SK", KeyFile: "/etc/gitstack/k"}
	if err := SetBackupS3(p, s); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Postgres.External || c.Postgres.Host != "db.example.com" || c.Postgres.Version != "16.4" {
		t.Errorf("postgres perso: %+v", c.Postgres)
	}
	if c.Backup.S3 == nil || c.Backup.S3.Bucket != "b" {
		t.Errorf("s3: %+v", c.Backup.S3)
	}
}
