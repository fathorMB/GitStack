package config

import "testing"

func TestLoad_SoglieDiDefault(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxBlobBytes != 100<<20 || cfg.RepoWarnBytes != 5<<30 || cfg.MirrorMaxFetchBytes != 5<<30 {
		t.Fatalf("soglie di default = %d, %d, %d", cfg.MaxBlobBytes, cfg.RepoWarnBytes, cfg.MirrorMaxFetchBytes)
	}
}

func TestLoad_SoglieDaAmbiente(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data", EnvMaxBlobSize: "50MB", EnvRepoSizeWarn: "0", EnvMirrorMaxFetch: "2GB"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxBlobBytes != 50<<20 || cfg.RepoWarnBytes != 0 || cfg.MirrorMaxFetchBytes != 2<<30 {
		t.Fatalf("soglie = %d, %d, %d", cfg.MaxBlobBytes, cfg.RepoWarnBytes, cfg.MirrorMaxFetchBytes)
	}
	if _, err := load(env(map[string]string{EnvDataDir: "/data", EnvMaxBlobSize: "tanto"})); err == nil {
		t.Fatal("una soglia non valida doveva dare errore")
	}
}

func TestParseSize(t *testing.T) {
	ok := map[string]int64{"0": 0, "123": 123, "1KB": 1024, "100 MB": 100 << 20, "5gb": 5 << 30, "2MiB": 2 << 20, "7b": 7}
	for in, want := range ok {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; atteso %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-1", "MB", "1.5GB", "9999999999TB", "abc"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) doveva fallire", in)
		}
	}
}

func TestLoad_ReceiveFsck(t *testing.T) {
	cfg, err := load(env(map[string]string{EnvDataDir: "/data"}))
	if err != nil || cfg.NoFsck {
		t.Fatalf("default: NoFsck=%v (%v)", cfg.NoFsck, err)
	}
	cfg, err = load(env(map[string]string{EnvDataDir: "/data", EnvReceiveFsck: "OFF"}))
	if err != nil || !cfg.NoFsck {
		t.Fatalf("off: NoFsck=%v (%v)", cfg.NoFsck, err)
	}
	if _, err := load(env(map[string]string{EnvDataDir: "/data", EnvReceiveFsck: "forse"})); err == nil {
		t.Fatal("valore non valido accettato")
	}
}
