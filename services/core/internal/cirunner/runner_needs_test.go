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

// workflowOf legge il workflow riscritto dal runner per act.
func workflowOf(t *testing.T, cfg cirunner.Config) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cfg.WorkDir, "workflow", "job.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRun_NeedsOutputsArrivanoAlJob (GIT-501): il task porta needs.build con esito e outputs; nel
// workflow passato ad act needs.build.outputs.digest e' il contesto vero (il valore c'e', con gli
// apici raddoppiati) e il log del job deploy non contiene righe del build.
func TestRun_NeedsOutputsArrivanoAlJob(t *testing.T) {
	wf := "name: image\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    outputs:\n      digest: ${{ steps.b.outputs.digest }}\n    steps:\n      - run: echo build\n        id: b\n" +
		"  deploy:\n    needs: build\n    if: needs.build.result == 'success'\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo \"deploy ${{ needs.build.outputs.digest }}\"\n"
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"}),
		extra: map[string]any{"job": "deploy"},
		needs: map[string]*runnerv1.TaskNeed{"build": {Result: runnerv1.Result_RESULT_SUCCESS, Outputs: map[string]string{"digest": "sha256:l'abc"}}}}
	cfg := cfgFor(t, start(t, g))
	t.Setenv("CIRUNNER_FAKE_ACT_PLAN", "1")
	res, err := cirunner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	log := strings.Join(g.rows, "\n")
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS || strings.Contains(log, "[image/build]") {
		t.Fatalf("esito %v, il log contiene il build:\n%s", res.Conclusion, log)
	}
	got := workflowOf(t, cfg)
	if !strings.Contains(got, `"digest":"sha256:l''abc"`) || !strings.Contains(got, ").build.outputs.digest }}") ||
		!strings.Contains(got, "if: fromJSON(") {
		t.Errorf("il contesto needs non e' nel workflow di act:\n%s", got)
	}
	if strings.Contains(got, "needs:") {
		t.Errorf("needs doveva essere tolto:\n%s", got)
	}
}

// TestRun_RiportaGliOutputsDelJob (GIT-501): il job build dichiara outputs; il runner aggiunge il
// passo di raccolta, riporta gli outputs con l'esito e il passo non compare ne' nel log ne' fra i passi.
func TestRun_RiportaGliOutputsDelJob(t *testing.T) {
	wf := "name: image\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    outputs:\n      digest: ${{ steps.b.outputs.digest }}\n      tag: ${{ steps.b.outputs.tag }}\n    steps:\n      - run: echo build\n        id: b\n"
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"})}
	cfg := cfgFor(t, start(t, g))
	t.Setenv("CIRUNNER_FAKE_ACT_EXPR", `{"steps.b.outputs.digest":"sha256:abc","steps.b.outputs.tag":"v1\n2"}`)
	res, err := cirunner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
		t.Fatalf("esito %v\n%s", res.Conclusion, strings.Join(g.rows, "\n"))
	}
	if len(g.outputs) != 2 || g.outputs["digest"] != "sha256:abc" || g.outputs["tag"] != "v1\n2" {
		t.Errorf("outputs riportati: %v", g.outputs)
	}
	if log := strings.Join(g.rows, "\n"); strings.Contains(log, "raccolta") || strings.Contains(log, "scrivo gli outputs") {
		t.Errorf("il passo di raccolta e' nel log:\n%s", log)
	}
	if len(g.steps) != 1 {
		t.Errorf("passi riportati %d, attesi 1\n%s", len(g.steps), strings.Join(g.rows, "\n"))
	}
}

// realAct ritorna il binario vero di act (GITSTACK_TEST_ACT), come TestRun_ActVero.
func realAct(t *testing.T) string {
	t.Helper()
	act := os.Getenv("GITSTACK_TEST_ACT")
	if act == "" {
		t.Skip("GITSTACK_TEST_ACT non impostata")
	}
	return act
}

// TestRun_ActVero_NeedsContext (GIT-501): con act vero il contesto needs passato dal task vale in
// if:, in ${{ }} con punto e con indice, in contains(needs.*.result, ..); il testo letterale di un
// run: non cambia e il build non viene rieseguito.
func TestRun_ActVero_NeedsContext(t *testing.T) {
	act := realAct(t)
	wf := `name: image
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build-rieseguito
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo lint-rieseguito
  deploy:
    needs: [build, lint]
    if: needs.build.result == 'success' && contains(needs.*.result, 'failure')
    runs-on: ubuntu-latest
    steps:
      - run: echo "D=${{ needs.build.outputs.digest }} I=${{ needs['build'].outputs['digest'] }} R=${{ needs['lint'].result }}"
      - run: echo "letterale needs.build.result"
`
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"}),
		extra: map[string]any{"job": "deploy"},
		needs: map[string]*runnerv1.TaskNeed{
			"build": {Result: runnerv1.Result_RESULT_SUCCESS, Outputs: map[string]string{"digest": "sha256:l'abc"}},
			"lint":  {Result: runnerv1.Result_RESULT_FAILURE},
		}}
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
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
		t.Fatalf("esito %v\n%s", res.Conclusion, log)
	}
	for _, want := range []string{"D=sha256:l'abc I=sha256:l'abc R=failure", "letterale needs.build.result"} {
		if !strings.Contains(log, want) {
			t.Errorf("manca %q nel log:\n%s", want, log)
		}
	}
	if strings.Contains(log, "rieseguito") {
		t.Errorf("i job a monte sono stati rieseguiti:\n%s", log)
	}
}

// TestRun_ActVero_RaccoltaOutputs (GIT-501): con act vero gli outputs dichiarati del job
// (jobs.<id>.outputs) arrivano al gateway e il passo di raccolta non e' fra i passi.
func TestRun_ActVero_RaccoltaOutputs(t *testing.T) {
	act := realAct(t)
	wf := `name: image
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    outputs:
      digest: ${{ steps.b.outputs.digest }}
      fisso: v-${{ steps.b.outputs.digest }}
    steps:
      - id: b
        run: echo "digest=sha256:xyz" >> "$GITHUB_OUTPUT"
        shell: bash
`
	g := &fakeGateway{workflow: wf, archive: tarball(t, map[string]string{"README.md": "x"})}
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
	if res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
		t.Fatalf("esito %v\n%s", res.Conclusion, log)
	}
	if g.outputs["digest"] != "sha256:xyz" || g.outputs["fisso"] != "v-sha256:xyz" {
		t.Errorf("outputs riportati: %v\n%s", g.outputs, log)
	}
	if len(g.steps) != 1 || strings.Contains(log, "raccolta") {
		t.Errorf("passi %d, il passo di raccolta non doveva comparire:\n%s", len(g.steps), log)
	}
}
