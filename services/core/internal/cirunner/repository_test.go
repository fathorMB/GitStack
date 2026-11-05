package cirunner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

// TestRun_RepositoryDelRun (GIT-563): il .git minimo del workspace ha il remote origin con
// owner/nome del repo del run, perche' e' da li' che act ricava github.repository e
// GITHUB_REPOSITORY (altrimenti ne sceglie uno suo e la release di un mirror da' 404).
func TestRun_RepositoryDelRun(t *testing.T) {
	for _, repo := range []string{"admin/odin", "alice/app.v2", "o-rg/re_po"} {
		g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "x"}),
			extra: map[string]any{"repository": repo, "ref": "refs/tags/v1.0.1"}}
		cfg := cfgFor(t, start(t, g))
		out := filepath.Join(t.TempDir(), "repo")
		t.Setenv("CIRUNNER_FAKE_ACT_REPO", out)
		res, err := cirunner.Run(context.Background(), cfg)
		if err != nil || res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
			t.Fatalf("%s: esito %+v: %v", repo, res, err)
		}
		if got, _ := os.ReadFile(out); string(got) != repo {
			t.Errorf("github.repository = %q, atteso %q", got, repo)
		}
	}
}

// TestRun_RepositoryNonValidoNonEntraInConfig: un valore che non e' owner/nome non finisce nel config.
func TestRun_RepositoryNonValidoNonEntraInConfig(t *testing.T) {
	for _, repo := range []string{"solo", "a/b/c", "a/../b", "a/b\n[core]", "a/b c", "/x"} {
		g := &fakeGateway{workflow: twoSteps, archive: tarball(t, map[string]string{"README.md": "x"}),
			extra: map[string]any{"repository": repo}}
		cfg := cfgFor(t, start(t, g))
		if _, err := cirunner.Run(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(cfg.WorkDir, "workspace", ".git", "config"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "remote") {
			t.Errorf("repository %q: config con un remote:\n%s", repo, b)
		}
	}
}

// TestRun_ActVero_Repository (GIT-563): con act vero, github.repository e GITHUB_REPOSITORY nei
// passi valgono owner/nome del repo del run.
func TestRun_ActVero_Repository(t *testing.T) {
	act := realAct(t)
	wf := "name: repo\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - shell: bash\n        run: echo \"CTX=${{ github.repository }} ENV=$GITHUB_REPOSITORY\"\n"
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"}),
		extra: map[string]any{"repository": "admin/odin", "ref": "refs/tags/v1.0.1"}}
	cfg := cfgFor(t, start(t, g))
	t.Setenv("CIRUNNER_FAKE_ACT", "")
	cfg.ActBin = act
	cfg.FetchTimeout = 5 * time.Second
	res, err := cirunner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	log := strings.Join(g.rows, "\n")
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS || !strings.Contains(log, "CTX=admin/odin ENV=admin/odin") {
		t.Fatalf("esito %v\n%s", res.Conclusion, log)
	}
	t.Logf("esito %v\n%s", res.Conclusion, log)
}
