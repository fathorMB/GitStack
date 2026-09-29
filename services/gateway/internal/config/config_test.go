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
		envCoreURL:               "http://core:8080",
		envIdentityURL:           "http://identity:8080",
		envIdentityTimeout:       "2s",
		envIdentityServiceSecret: "segreto",
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

func TestLoad_AuthSecretECache(t *testing.T) {
	base := map[string]string{envCoreURL: "http://core:8080"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	// Con identity il segreto di servizio è obbligatorio.
	if _, err := load(lookupFrom(with(envIdentityURL, "http://identity:8080"))); err == nil {
		t.Error("IdentityURL senza segreto di servizio deve dare errore")
	}

	def, err := load(lookupFrom(with()))
	if err != nil {
		t.Fatal(err)
	}
	if def.AuthCacheTTL != 30*time.Second || def.AuthCacheNegativeTTL != 5*time.Second {
		t.Errorf("TTL di default = %v/%v, voluti 30s/5s", def.AuthCacheTTL, def.AuthCacheNegativeTTL)
	}

	cfg, err := load(lookupFrom(with(
		envIdentityURL, "http://identity:8080", envIdentityServiceSecret, " s3gr3t0 ",
		envAuthCacheTTL, "10s", envAuthCacheNegativeTTL, "2s")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdentityServiceSecret != "s3gr3t0" || cfg.AuthCacheTTL != 10*time.Second || cfg.AuthCacheNegativeTTL != 2*time.Second {
		t.Errorf("cfg = %+v", cfg)
	}

	for _, env := range []string{envAuthCacheTTL, envAuthCacheNegativeTTL} {
		for _, bad := range []string{"abc", "0s", "-1s"} {
			if _, err := load(lookupFrom(with(env, bad))); err == nil {
				t.Errorf("%s=%q accettato", env, bad)
			}
		}
	}
}
