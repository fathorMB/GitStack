package config

import (
	"strings"
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

func TestLoad_TrustedProxiesESessionTTL(t *testing.T) {
	base := "postgres://identity:secret@localhost:5432/gitstack"
	cfg, err := load(lookupFrom(map[string]string{EnvDatabaseURL: base}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 || cfg.SessionTTL != defaultSessionTTL {
		t.Errorf("default inattesi: %v %s", cfg.TrustedProxies, cfg.SessionTTL)
	}
	cfg, err = load(lookupFrom(map[string]string{
		EnvDatabaseURL: base, EnvTrustedProxies: "10.42.0.0/16, fd00::/8", EnvSessionTTL: "2h",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.SessionTTL != 2*time.Hour {
		t.Errorf("valori inattesi: %v %s", cfg.TrustedProxies, cfg.SessionTTL)
	}
	for _, env := range []map[string]string{
		{EnvDatabaseURL: base, EnvTrustedProxies: "10.0.0.1"},
		{EnvDatabaseURL: base, EnvSessionTTL: "-1h"},
	} {
		if _, err := load(lookupFrom(env)); err == nil {
			t.Errorf("attendevo un errore per %v", env)
		}
	}
}

func TestLoad_ServiceSecretETokenMaxLifetime(t *testing.T) {
	env := map[string]string{EnvDatabaseURL: "postgres://u:p@h/db"}
	get := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	cfg, err := load(get)
	if err != nil || cfg.ServiceSecret != "" || cfg.TokenMaxLifetime != 0 {
		t.Fatalf("default: err=%v secret=%q max=%v", err, cfg.ServiceSecret, cfg.TokenMaxLifetime)
	}

	env[EnvServiceSecret] = "  segreto-123  "
	env[EnvTokenMaxLifetime] = "720h"
	cfg, err = load(get)
	if err != nil || cfg.ServiceSecret != "segreto-123" || cfg.TokenMaxLifetime != 720*time.Hour {
		t.Fatalf("override: err=%v secret=%q max=%v", err, cfg.ServiceSecret, cfg.TokenMaxLifetime)
	}

	// Un errore di configurazione non deve mai riportare il segreto.
	env[EnvTokenMaxLifetime] = "boh"
	_, err = load(get)
	if err == nil {
		t.Fatal("durata non valida accettata")
	}
	if strings.Contains(err.Error(), "segreto-123") {
		t.Errorf("il segreto compare nell'errore: %v", err)
	}
}
