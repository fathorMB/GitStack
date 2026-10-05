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
