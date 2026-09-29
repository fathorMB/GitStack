package config

import (
	"testing"
	"time"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envCoreURL: "http://core:8080",
	}))
	if err != nil {
		t.Fatalf("load() errore inatteso: %v", err)
	}
	if cfg.Addr != defaultAddr {
		t.Errorf("Addr = %q, voluto %q", cfg.Addr, defaultAddr)
	}
	if cfg.CoreTimeout != defaultCoreTimeout {
		t.Errorf("CoreTimeout = %v, voluto %v", cfg.CoreTimeout, defaultCoreTimeout)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, voluto %q", cfg.LogLevel, defaultLogLevel)
	}
	if cfg.CoreURL == nil || cfg.CoreURL.String() != "http://core:8080" {
		t.Errorf("CoreURL = %v, voluto http://core:8080", cfg.CoreURL)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envAddr:        ":9090",
		envCoreURL:     "http://core.internal:9000",
		envCoreTimeout: "2500ms",
		envLogLevel:    "DEBUG",
	}))
	if err != nil {
		t.Fatalf("load() errore inatteso: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr = %q, voluto :9090", cfg.Addr)
	}
	if cfg.CoreTimeout != 2500*time.Millisecond {
		t.Errorf("CoreTimeout = %v, voluto 2500ms", cfg.CoreTimeout)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, voluto debug (case-insensitive)", cfg.LogLevel)
	}
}

func TestLoad_CoreURLMancante(t *testing.T) {
	_, err := load(lookupFrom(map[string]string{}))
	if err == nil {
		t.Fatal("load() doveva fallire senza GITSTACK_CORE_URL")
	}
}

func TestLoad_CoreURLNonValida(t *testing.T) {
	for _, v := range []string{"non-una-url", "/solo/un/path", "://vuoto"} {
		_, err := load(lookupFrom(map[string]string{envCoreURL: v}))
		if err == nil {
			t.Errorf("load() doveva fallire con CoreURL=%q", v)
		}
	}
}

func TestLoad_CoreTimeoutNonValido(t *testing.T) {
	_, err := load(lookupFrom(map[string]string{
		envCoreURL:     "http://core:8080",
		envCoreTimeout: "non-una-durata",
	}))
	if err == nil {
		t.Fatal("load() doveva fallire con CoreTimeout non valido")
	}
}

func TestLoad_LogLevelNonValido(t *testing.T) {
	_, err := load(lookupFrom(map[string]string{
		envCoreURL:  "http://core:8080",
		envLogLevel: "verbosissimo",
	}))
	if err == nil {
		t.Fatal("load() doveva fallire con LogLevel non valido")
	}
}

func TestLoad_TrustedProxies(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envCoreURL:        "http://core:8080",
		envTrustedProxies: "10.42.0.0/16, 192.168.1.0/24,",
	}))
	if err != nil {
		t.Fatalf("load() errore inatteso: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "10.42.0.0/16" {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}

	cfg, err = load(lookupFrom(map[string]string{envCoreURL: "http://core:8080"}))
	if err != nil || len(cfg.TrustedProxies) != 0 {
		t.Errorf("default: err=%v proxies=%v, voluto nessuno", err, cfg.TrustedProxies)
	}

	if _, err = load(lookupFrom(map[string]string{envCoreURL: "http://core:8080", envTrustedProxies: "non-un-cidr"})); err == nil {
		t.Error("CIDR non valido accettato")
	}
}

func TestLoad_Identity(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envCoreURL:         "http://core:8080",
		envIdentityURL:     "http://identity:8080",
		envIdentityTimeout: "2s",
	}))
	if err != nil {
		t.Fatalf("load() errore inatteso: %v", err)
	}
	if cfg.IdentityURL == nil || cfg.IdentityURL.String() != "http://identity:8080" {
		t.Errorf("IdentityURL = %v", cfg.IdentityURL)
	}
	if cfg.IdentityTimeout != 2*time.Second {
		t.Errorf("IdentityTimeout = %v, voluto 2s", cfg.IdentityTimeout)
	}

	def, err := load(lookupFrom(map[string]string{envCoreURL: "http://core:8080"}))
	if err != nil {
		t.Fatalf("load() errore inatteso: %v", err)
	}
	if def.IdentityURL != nil || def.IdentityTimeout != defaultIdentityTimeout {
		t.Errorf("default identity = %v/%v", def.IdentityURL, def.IdentityTimeout)
	}

	if _, err := load(lookupFrom(map[string]string{envCoreURL: "http://core:8080", envIdentityURL: "identity"})); err == nil {
		t.Error("una IdentityURL non assoluta deve dare errore")
	}
}
