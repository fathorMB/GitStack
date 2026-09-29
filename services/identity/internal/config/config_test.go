package config

import (
	"testing"
	"time"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		EnvDatabaseURL: "postgres://identity:secret@localhost:5432/gitstack?sslmode=disable",
	}))
	if err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if cfg.Addr != defaultAddr {
		t.Errorf("Addr = %q, voluto %q", cfg.Addr, defaultAddr)
	}
	if cfg.DBMaxConns != defaultDBMaxConns {
		t.Errorf("DBMaxConns = %d, voluto %d", cfg.DBMaxConns, defaultDBMaxConns)
	}
	if cfg.MigrationsTimeout != defaultMigrationsTimeout {
		t.Errorf("MigrationsTimeout = %s, voluto %s", cfg.MigrationsTimeout, defaultMigrationsTimeout)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, voluto %q", cfg.LogLevel, defaultLogLevel)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	_, err := load(lookupFrom(map[string]string{}))
	if err == nil {
		t.Fatal("attendevo un errore per GITSTACK_IDENTITY_DB_URL mancante")
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		EnvAddr:              ":9090",
		EnvDatabaseURL:       "postgres://identity:secret@localhost:5432/gitstack?sslmode=disable",
		EnvDBMaxConns:        "25",
		EnvMigrationsTimeout: "45s",
		EnvLogLevel:          "DEBUG",
	}))
	if err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr = %q, voluto :9090", cfg.Addr)
	}
	if cfg.DBMaxConns != 25 {
		t.Errorf("DBMaxConns = %d, voluto 25", cfg.DBMaxConns)
	}
	if cfg.MigrationsTimeout != 45*time.Second {
		t.Errorf("MigrationsTimeout = %s, voluto 45s", cfg.MigrationsTimeout)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, voluto debug", cfg.LogLevel)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"db max conns non numerico": {
			EnvDatabaseURL: "postgres://identity:secret@localhost:5432/gitstack",
			EnvDBMaxConns:  "abc",
		},
		"migrations timeout non valido": {
			EnvDatabaseURL:       "postgres://identity:secret@localhost:5432/gitstack",
			EnvMigrationsTimeout: "abc",
		},
		"log level non valido": {
			EnvDatabaseURL: "postgres://identity:secret@localhost:5432/gitstack",
			EnvLogLevel:    "verbose",
		},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(lookupFrom(env)); err == nil {
				t.Fatal("attendevo un errore")
			}
		})
	}
}
