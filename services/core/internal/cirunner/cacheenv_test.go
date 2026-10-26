package cirunner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

// TestRun_AmbienteDelJobPerCacheEArtefatti (GIT-330): act riceve ACTIONS_CACHE_URL e
// ACTIONS_RUNTIME_TOKEN, col token del job (mai il segreto del runner) e con l'indirizzo con cui
// il pod raggiunge il gateway. Run è lo stesso per il pod interno e per il runner esterno (Serve).
func TestRun_AmbienteDelJobPerCacheEArtefatti(t *testing.T) {
	g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "x"})}
	url := start(t, g)
	cfg := cfgFor(t, url)
	envFile := filepath.Join(t.TempDir(), "env.txt")
	t.Setenv("CIRUNNER_FAKE_ACT_ENV", envFile)
	if _, err := cirunner.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	if got["ACTIONS_CACHE_URL"] != url+"/ci/v1/cache/" {
		t.Errorf("ACTIONS_CACHE_URL = %q", got["ACTIONS_CACHE_URL"])
	}
	if got["ACTIONS_RESULTS_URL"] != url+"/" {
		t.Errorf("ACTIONS_RESULTS_URL = %q", got["ACTIONS_RESULTS_URL"])
	}
	if got["ACTIONS_RUNTIME_TOKEN"] != jobToken || got["ACTIONS_RUNTIME_TOKEN"] == secret {
		t.Errorf("ACTIONS_RUNTIME_TOKEN = %q", got["ACTIONS_RUNTIME_TOKEN"])
	}
	// GIT-371: i passi (gitstack-build) leggono il repo vero del job, non quello che act deduce.
	if got["GITSTACK_REPOSITORY"] != "alice/app" {
		t.Errorf("GITSTACK_REPOSITORY = %q", got["GITSTACK_REPOSITORY"])
	}
}

func TestCacheEnv_UsaLIndirizzoDellArchivio(t *testing.T) {
	env := cirunner.CacheEnv("http://ci-gateway.ns:8081/ci/v1/archive", "tok")
	want := []string{"ACTIONS_CACHE_URL=http://ci-gateway.ns:8081/ci/v1/cache/", "ACTIONS_RESULTS_URL=http://ci-gateway.ns:8081/", "ACTIONS_RUNTIME_TOKEN=tok"}
	if strings.Join(env, "|") != strings.Join(want, "|") {
		t.Errorf("env = %v", env)
	}
}
