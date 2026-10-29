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

// TestUpdateTask_OutputsSenzaEsitoEConfermati (GIT-501, act_runner di Gitea): gli outputs arrivano
// in un UpdateTask senza esito, il gateway li salva e li conferma in SentOutputs (anche quelli
// scartati per i limiti); l'UpdateTask finale, con l'esito e senza outputs, li lascia intatti e il
// task del job a valle li porta in needs.
func TestUpdateTask_OutputsSenzaEsitoEConfermati(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queue("app", "a")
	ctx := context.Background()
	t1, err := e.fetch(runnerSecret)
	if err != nil || t1 == nil {
		t.Fatalf("fetch: %v %v", t1, err)
	}
	tok := taskToken(t, t1)
	tooLong := strings.Repeat("n", ci.MaxOutputNameLen+1)
	res, err := e.client.UpdateTask(ctx, authed(&runnerv1.UpdateTaskRequest{
		State:   &runnerv1.TaskState{Id: t1.GetId()},
		Outputs: map[string]string{"digest": "sha256:abc", tooLong: "x"}}, tok))
	if err != nil {
		t.Fatal(err)
	}
	sent := map[string]bool{}
	for _, k := range res.Msg.GetSentOutputs() {
		sent[k] = true
	}
	if !sent["digest"] || !sent[tooLong] || len(sent) != 2 {
		t.Errorf("SentOutputs: %v", res.Msg.GetSentOutputs())
	}
	var raw string
	job := e.jobID(runID, "build")
	if err := e.pool.QueryRow(ctx, `SELECT outputs::text FROM core.ci_jobs WHERE id = $1`, job).Scan(&raw); err != nil || !strings.Contains(raw, "sha256:abc") || strings.Contains(raw, "nnnn") {
		t.Fatalf("dopo l'invio senza esito: %s %v", raw, err)
	}
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM core.ci_jobs WHERE id = $1`, job).Scan(&status); err != nil || status != "in_progress" {
		t.Fatalf("il job doveva restare in corso: %s %v", status, err)
	}
	if err := e.update(tok, t1.GetId(), runnerv1.Result_RESULT_SUCCESS); err != nil {
		t.Fatal(err)
	}
	t2, err := e.fetch(runnerSecret)
	if err != nil || t2 == nil {
		t.Fatalf("secondo job: %v %v", t2, err)
	}
	if t2.GetNeeds()["build"].GetOutputs()["digest"] != "sha256:abc" {
		t.Errorf("needs del task: %v", t2.GetNeeds())
	}
}

// TestUpdateTask_OutputsParzialiSiUniscono (GIT-501): due invii parziali (a, poi b) lasciano
// entrambe le chiavi; una chiave gia' presente si sovrascrive; il limite vale sul totale.
func TestUpdateTask_OutputsParzialiSiUniscono(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queue("app", "a")
	ctx := context.Background()
	t1, err := e.fetch(runnerSecret)
	if err != nil || t1 == nil {
		t.Fatalf("fetch: %v %v", t1, err)
	}
	tok := taskToken(t, t1)
	send := func(m map[string]string) {
		t.Helper()
		if _, err := e.client.UpdateTask(ctx, authed(&runnerv1.UpdateTaskRequest{
			State: &runnerv1.TaskState{Id: t1.GetId()}, Outputs: m}, tok)); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]string{"a": "1"})
	send(map[string]string{"b": "2", "a": "3"})
	many := map[string]string{}
	for i := 0; i < ci.MaxJobOutputs; i++ {
		many["k"+strings.Repeat("x", i%5)+string(rune('A'+i/5))+string(rune('a'+i%5))] = "v"
	}
	send(many)
	var a, b string
	var n int
	job := e.jobID(runID, "build")
	if err := e.pool.QueryRow(ctx, `SELECT outputs->>'a', outputs->>'b', (SELECT count(*) FROM jsonb_object_keys(outputs)) FROM core.ci_jobs WHERE id = $1`, job).Scan(&a, &b, &n); err != nil {
		t.Fatal(err)
	}
	if a != "3" || b != "2" || n != ci.MaxJobOutputs {
		t.Errorf("a=%q b=%q chiavi=%d (attese 3, 2, %d)", a, b, n, ci.MaxJobOutputs)
	}
}
