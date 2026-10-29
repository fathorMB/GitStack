//go:build integration

package cigateway_test

import (
	"context"
	"strings"
	"testing"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
)

// TestUpdateTask_OutputsPassatiAlJobAValle (GIT-501): gli outputs riportati con l'esito finale
// del build sono salvati su ci_jobs e il task del job test (needs: build) li porta nel campo
// needs insieme all'esito. Un valore uguale al token del job e' mascherato; i nomi oltre il
// limite sono scartati.
func TestUpdateTask_OutputsPassatiAlJobAValle(t *testing.T) {
	e := newEnv(t)
	repo, runID := e.queue("app", "a")
	ctx := context.Background()

	t1, err := e.fetch(runnerSecret)
	if err != nil || t1 == nil {
		t.Fatalf("fetch: %v %v", t1, err)
	}
	if len(t1.GetNeeds()) != 0 {
		t.Errorf("il job senza needs ha needs: %v", t1.GetNeeds())
	}
	tok1 := taskToken(t, t1)
	outs := map[string]string{"digest": "sha256:abc", "multi": "a\nb", "segreto": "x-" + tok1, strings.Repeat("n", ci.MaxOutputNameLen+1): "troppo lungo"}
	if _, err := e.client.UpdateTask(ctx, authed(&runnerv1.UpdateTaskRequest{
		State:   &runnerv1.TaskState{Id: t1.GetId(), Result: runnerv1.Result_RESULT_SUCCESS},
		Outputs: outs}, tok1)); err != nil {
		t.Fatal(err)
	}

	var raw string
	if err := e.pool.QueryRow(ctx, `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, e.jobID(runID, "build")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"digest": "sha256:abc"`) || strings.Contains(raw, tok1) || strings.Contains(raw, "troppo") {
		t.Errorf("outputs salvati: %s", raw)
	}

	t2, err := e.fetch(runnerSecret)
	if err != nil || t2 == nil || t2.GetContext().AsMap()["job"] != "test" {
		t.Fatalf("secondo job: %v %v", t2, err)
	}
	need := t2.GetNeeds()["build"]
	if need == nil || need.GetResult() != runnerv1.Result_RESULT_SUCCESS {
		t.Fatalf("needs del task: %v", t2.GetNeeds())
	}
	if need.GetOutputs()["digest"] != "sha256:abc" || need.GetOutputs()["multi"] != "a\nb" || len(need.GetOutputs()) != 3 {
		t.Errorf("outputs nel task: %v", need.GetOutputs())
	}
	if strings.Contains(need.GetOutputs()["segreto"], tok1) {
		t.Errorf("il token del job e' negli outputs: %v", need.GetOutputs())
	}
	_ = repo
}

// TestUpdateTask_SenzaOutputsNessunoSalvato: senza outputs la colonna resta {} e il task del job a
// valle porta comunque l esito del build.
func TestUpdateTask_SenzaOutputsNessunoSalvato(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queue("app", "a")
	ctx := context.Background()
	t1, err := e.fetch(runnerSecret)
	if err != nil || t1 == nil {
		t.Fatalf("fetch: %v %v", t1, err)
	}
	if err := e.update(taskToken(t, t1), t1.GetId(), runnerv1.Result_RESULT_SUCCESS); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := e.pool.QueryRow(ctx, `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, e.jobID(runID, "build")).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" {
		t.Errorf("outputs: %s", raw)
	}
	t2, err := e.fetch(runnerSecret)
	if err != nil || t2 == nil {
		t.Fatalf("secondo job: %v %v", t2, err)
	}
	need := t2.GetNeeds()["build"]
	if need == nil || need.GetResult() != runnerv1.Result_RESULT_SUCCESS || len(need.GetOutputs()) != 0 {
		t.Errorf("needs del task: %v", t2.GetNeeds())
	}
}
