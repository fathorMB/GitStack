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
		envDatabaseURL: "postgres://core:secret@localhost:5432/gitstack?sslmode=disable",
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
	// NatsURL non è validata come obbligatoria da load(): serve solo al
	// comando "serve" (verificato lì, non qui), non a "migrate up|down".
	if cfg.NatsURL != "" {
		t.Errorf("NatsURL = %q, voluto vuoto senza GITSTACK_CORE_NATS_URL", cfg.NatsURL)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, voluto %q", cfg.LogLevel, defaultLogLevel)
	}
}

func TestLoad_ServiceSecret(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envDatabaseURL:   "postgres://core:secret@localhost:5432/gitstack?sslmode=disable",
		envServiceSecret: " s3gr3t0 ",
	}))
	if err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if cfg.ServiceSecret != "s3gr3t0" {
		t.Errorf("ServiceSecret = %q", cfg.ServiceSecret)
	}
	// Come NatsURL, è richiesto dal solo comando serve (main.go), non da load.
	cfg, err = load(lookupFrom(map[string]string{envDatabaseURL: "postgres://x"}))
	if err != nil || cfg.ServiceSecret != "" {
		t.Errorf("senza segreto: err=%v secret=%q", err, cfg.ServiceSecret)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	_, err := load(lookupFrom(map[string]string{}))
	if err == nil {
		t.Fatal("attendevo un errore per GITSTACK_CORE_DB_URL mancante")
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envAddr:              ":9090",
		envDatabaseURL:       "postgres://core:secret@localhost:5432/gitstack?sslmode=disable",
		envDBMaxConns:        "25",
		envMigrationsTimeout: "45s",
		envNatsURL:           "nats://nats:4222",
		envLogLevel:          "DEBUG",
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
	if cfg.NatsURL != "nats://nats:4222" {
		t.Errorf("NatsURL = %q, voluto nats://nats:4222", cfg.NatsURL)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, voluto debug", cfg.LogLevel)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"db max conns non numerico": {
			envDatabaseURL: "postgres://core:secret@localhost:5432/gitstack",
			envNatsURL:     "nats://localhost:4222",
			envDBMaxConns:  "abc",
		},
		"migrations timeout non valido": {
			envDatabaseURL:       "postgres://core:secret@localhost:5432/gitstack",
			envNatsURL:           "nats://localhost:4222",
			envMigrationsTimeout: "abc",
		},
		"log level non valido": {
			envDatabaseURL: "postgres://core:secret@localhost:5432/gitstack",
			envNatsURL:     "nats://localhost:4222",
			envLogLevel:    "verbose",
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

func TestLoad_IdentityURL(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envDatabaseURL: "postgres://x", envIdentityURL: " http://identity:8080 ",
	}))
	if err != nil || cfg.IdentityURL != "http://identity:8080" {
		t.Errorf("IdentityURL = %q, err=%v", cfg.IdentityURL, err)
	}
	cfg, err = load(lookupFrom(map[string]string{envDatabaseURL: "postgres://x"}))
	if err != nil || cfg.IdentityURL != "" {
		t.Errorf("senza URL: err=%v url=%q", err, cfg.IdentityURL)
	}
}
