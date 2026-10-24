package config

import (
	"strings"
	"testing"
)

// Artefatti e cache della CI (GIT-280, CI5): disco di default, S3 se c'è il bucket (e allora servono endpoint e chiavi).
func TestLoad_CIStorage(t *testing.T) {
	base := map[string]string{envDatabaseURL: "postgres://x"}
	cfg, err := load(lookupFrom(base))
	if err != nil || cfg.CIArtifactsDir != "" || cfg.CIS3.Enabled() {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	if envCIArtifactsDir != "GITSTACK_CORE_CI_ARTIFACTS_DIR" || envCIS3Bucket != "GITSTACK_CORE_CI_S3_BUCKET" ||
		envCIS3Endpoint != "GITSTACK_CORE_CI_S3_ENDPOINT" || envCIS3AccessKey != "GITSTACK_CORE_CI_S3_ACCESS_KEY" || envCIS3SecretKey != "GITSTACK_CORE_CI_S3_SECRET_KEY" {
		t.Fatal("nomi delle variabili cambiati: li usa il chart")
	}
	base[envCIArtifactsDir] = " /var/lib/gitstack/ci/data "
	if cfg, err = load(lookupFrom(base)); err != nil || cfg.CIArtifactsDir != "/var/lib/gitstack/ci/data" || cfg.CIS3.Enabled() {
		t.Fatalf("disco: %+v %v", cfg, err)
	}
	base[envCIS3Bucket] = "ci"
	if _, err = load(lookupFrom(base)); err == nil || !strings.Contains(err.Error(), envCIS3Endpoint) {
		t.Fatalf("il bucket senza endpoint e chiavi è un errore: %v", err)
	}
	base[envCIS3Endpoint] = "https://s3.example.org"
	base[envCIS3AccessKey] = "AK"
	base[envCIS3SecretKey] = "SK"
	base[envCIS3Region] = "eu-west-1"
	base[envCIS3Prefix] = "gs/"
	cfg, err = load(lookupFrom(base))
	if err != nil || !cfg.CIS3.Enabled() || cfg.CIS3.Endpoint != "https://s3.example.org" || cfg.CIS3.Region != "eu-west-1" || cfg.CIS3.Prefix != "gs/" || cfg.CIS3.SecretKey != "SK" {
		t.Fatalf("S3: %+v %v", cfg.CIS3, err)
	}
}
