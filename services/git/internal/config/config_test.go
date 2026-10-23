package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoad_Default(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data", EnvServiceSecret: " s3 "}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.LogLevel != "info" || cfg.ServiceSecret != "s3" || cfg.DataDir == "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoad_Errori(t *testing.T) {
	_, err := load(env(map[string]string{EnvLogLevel: "boh", EnvServiceSecret: "segretissimo"}))
	if err == nil {
		t.Fatal("attesi errori")
	}
	msg := err.Error()
	if !strings.Contains(msg, EnvDataDir) || !strings.Contains(msg, EnvLogLevel) {
		t.Fatalf("errore incompleto: %v", msg)
	}
	if strings.Contains(msg, "segretissimo") {
		t.Fatal("il segreto compare nell'errore")
	}
}

func TestLoad_SSH(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHAddr != ":2222" || cfg.SSHHostKeyFile == "" || cfg.IdentityURL != "" {
		t.Fatalf("default SSH: %+v", cfg)
	}
	cfg, err = load(env(map[string]string{
		EnvDataDir: "/data", EnvSSHAddr: " :2200 ", EnvSSHHostKey: "/etc/gitstack/ssh/ssh_host_ed25519_key",
		EnvIdentityURL: "http://identity:8080", EnvCoreURL: "http://core:8080",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHAddr != ":2200" || cfg.SSHHostKeyFile != "/etc/gitstack/ssh/ssh_host_ed25519_key" ||
		cfg.IdentityURL != "http://identity:8080" || cfg.CoreURL != "http://core:8080" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.NatsURL != "" {
		t.Fatalf("NATS non configurato di default: %q", cfg.NatsURL)
	}
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvNatsURL: " nats://nats:4222 "}))
	if err != nil || cfg.NatsURL != "nats://nats:4222" {
		t.Fatalf("NATS: %+v, %v", cfg, err)
	}
	if cfg.MirrorCAFile != "" {
		t.Fatalf("CA dei mirror non configurata di default: %q", cfg.MirrorCAFile)
	}
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvMirrorCAFile: " /etc/gitstack/mirror-ca/ca.crt "}))
	if err != nil || cfg.MirrorCAFile != "/etc/gitstack/mirror-ca/ca.crt" {
		t.Fatalf("CA dei mirror: %+v, %v", cfg, err)
	}
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvSSHAddr: "OFF"}))
	if err != nil || cfg.SSHAddr != "" {
		t.Fatalf("off: %+v, %v", cfg, err)
	}
}

func TestLoad_SegretiPerChiamante(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data", EnvSecretGit: "kgit", EnvSecretCore: "kc1,kc0", EnvServiceSecret: "storico"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceSecret != "kgit" || cfg.CoreSecret != "kc1,kc0" {
		t.Fatalf("segreti = %q %q", cfg.ServiceSecret, cfg.CoreSecret)
	}
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvSecretGit: "kgit"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceSecret != "kgit" || cfg.CoreSecret != "" {
		t.Fatalf("senza segreto di core: %q %q", cfg.ServiceSecret, cfg.CoreSecret)
	}
}

func TestLoad_RegistryQuotaEPulizia(t *testing.T) {
	base := map[string]string{EnvDataDir: "/data", EnvServiceSecret: "s3"}
	get := func(extra map[string]string) (Config, error) {
		return load(func(k string) (string, bool) {
			if v, ok := extra[k]; ok {
				return v, true
			}
			v, ok := base[k]
			return v, ok
		})
	}
	cfg, err := get(nil)
	if err != nil || cfg.RegistryQuotaBytes != 0 || cfg.RegistryCleanupEvery != time.Hour {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	cfg, err = get(map[string]string{EnvRegistryQuota: "10GB", EnvRegistryCleanupEvery: "30m"})
	if err != nil || cfg.RegistryQuotaBytes != 10<<30 || cfg.RegistryCleanupEvery != 30*time.Minute {
		t.Fatalf("valori: %+v %v", cfg, err)
	}
	if _, err := get(map[string]string{EnvRegistryQuota: "tanto"}); err == nil {
		t.Error("quota non valida accettata")
	}
	if _, err := get(map[string]string{EnvRegistryCleanupEvery: "-1h"}); err == nil {
		t.Error("intervallo negativo accettato")
	}
}
