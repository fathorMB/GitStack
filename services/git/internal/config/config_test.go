package config

import (
	"strings"
	"testing"
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
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvSSHAddr: "OFF"}))
	if err != nil || cfg.SSHAddr != "" {
		t.Fatalf("off: %+v, %v", cfg, err)
	}
}
