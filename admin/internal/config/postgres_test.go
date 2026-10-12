package config

import (
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
