package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_Registry(t *testing.T) {
	base := map[string]string{EnvDataDir: "/data"}
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

	cfg, err := load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RegistryOff || cfg.RegistryS3 != nil || cfg.CIGatewayURL != "" || filepath.Base(cfg.RegistryDir) != "registry" || filepath.Dir(cfg.RegistryDir) != cfg.DataDir {
		t.Errorf("default: %+v", cfg)
	}

	cfg, err = load(env(with(EnvRegistry, "off")))
	if err != nil || !cfg.RegistryOff {
		t.Errorf("off: %v %+v", err, cfg)
	}
	cfg, err = load(env(with(EnvRegistryDir, "/altro", EnvCIGatewayURL, " http://core:9090 ")))
	if err != nil || cfg.RegistryDir != "/altro" || cfg.CIGatewayURL != "http://core:9090" {
		t.Errorf("dir e ci-gateway: %v %+v", err, cfg)
	}

	cfg, err = load(env(with(
		EnvRegistryS3Bucket, "img", EnvRegistryS3Region, "eu-west-1", EnvRegistryS3Endpoint, "http://minio:9000",
		EnvRegistryS3AccessKey, "AK", EnvRegistryS3SecretKey, "SEGRETISSIMO", EnvRegistryS3PathStyle, "on", EnvRegistryS3Prefix, "reg")))
	if err != nil || cfg.RegistryS3 == nil || *cfg.RegistryS3 != (RegistryS3{Bucket: "img", Region: "eu-west-1", Endpoint: "http://minio:9000", AccessKey: "AK", SecretKey: "SEGRETISSIMO", Prefix: "reg", PathStyle: true}) {
		t.Errorf("S3: %v %+v", err, cfg.RegistryS3)
	}

	for name, m := range map[string]map[string]string{
		"registry non on/off":     with(EnvRegistry, "forse"),
		"S3 senza bucket":         with(EnvRegistryS3Endpoint, "http://minio:9000"),
		"chiave senza segreto":    with(EnvRegistryS3Bucket, "b", EnvRegistryS3AccessKey, "AK"),
		"path style non on/off":   with(EnvRegistryS3Bucket, "b", EnvRegistryS3PathStyle, "si"),
		"segreto senza la chiave": with(EnvRegistryS3Bucket, "b", EnvRegistryS3SecretKey, "SEGRETISSIMO"),
	} {
		_, err := load(env(m))
		if err == nil {
			t.Errorf("%s: doveva fallire", name)
		} else if strings.Contains(err.Error(), "SEGRETISSIMO") {
			t.Errorf("%s: il segreto è nell'errore", name)
		}
	}
}
