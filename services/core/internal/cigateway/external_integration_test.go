//go:build integration

package cigateway_test

import (
	"context"
	"strings"
	"testing"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const externalWF = `name: ext
on: push
jobs:
  linux:
    runs-on: [self-hosted, linux]
    steps:
      - run: echo uno
  gpu:
    runs-on: [self-hosted, gpu]
    steps:
      - run: echo gpu
  pod:
    runs-on: ubuntu-latest
    steps:
      - run: echo pod
`

func (e *env) queueWF(name, commit, wf string) (uuid.UUID, uuid.UUID) {
	e.t.Helper()
	repo := e.repo(name)
	e.src.files[sha(commit)] = []ci.File{{Path: ".github/workflows/ci.yml", Content: []byte(wf)}}
	ids, err := e.eng.OnPush(context.Background(), ci.Push{RepoID: repo, Pusher: uuid.New(),
		Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("0"), After: sha(commit)}}})
	if err != nil || len(ids) != 1 {
		e.t.Fatalf("push: %v %v", ids, err)
	}
	return repo, ids[0]
}

// register emette un token di registrazione e registra un runner; ritorna il token del runner.
func (e *env) register(name string, labels ...string) (string, error) {
	e.t.Helper()
	reg, _, err := ci.IssueRegistrationToken(context.Background(), e.pool, e.now(), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.registerWith(reg, name, labels...)
}

func (e *env) registerWith(reg, name string, labels ...string) (string, error) {
	res, err := e.client.Register(context.Background(), connect.NewRequest(&runnerv1.RegisterRequest{
		Name: name, Token: reg, Version: "t1", Labels: labels}))
	if err != nil {
		return "", err
	}
	return res.Msg.GetRunner().GetToken(), nil
}

func (e *env) jobStatus(runID uuid.UUID, key string) (status, runner string) {
	e.t.Helper()
	var rn *string
	if err := e.pool.QueryRow(context.Background(), `SELECT status, runner_name FROM core.ci_jobs WHERE id = $1`, e.jobID(runID, key)).Scan(&status, &rn); err != nil {
		e.t.Fatal(err)
	}
	if rn != nil {
		runner = *rn
	}
	return
}

// TestExternal_RegistrazioneERunsOn (GIT-278): il token di registrazione vale una volta
// sola; il runner prende solo i job il cui runs-on è coperto dalle sue etichette, uno per
// volta; un job con un'etichetta che nessun runner ha resta in coda; il pod interno non
// finisce al runner esterno.
func TestExternal_RegistrazioneERunsOn(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queueWF("app", "a", externalWF)

	reg, _, err := ci.IssueRegistrationToken(context.Background(), e.pool, e.now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := e.registerWith(reg, "r1", "Linux:host")
	if err != nil || !strings.HasPrefix(rt, ci.RunnerTokenPrefix) {
		t.Fatalf("registrazione: %q %v", rt, err)
	}
	// Monouso.
	if _, err := e.registerWith(reg, "r2", "linux"); code(err) != connect.CodeUnauthenticated {
		t.Errorf("secondo uso del token: %v", err)
	}
	// Token inventato o con il prefisso del token del runner.
	for _, bad := range []string{"", "x", ci.RunnerTokenPrefix + "abc", ci.RegistrationTokenPrefix + "abc"} {
		if _, err := e.registerWith(bad, "r3"); code(err) != connect.CodeUnauthenticated {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	// Un nome già usato non consuma il token.
	reg2, _, _ := ci.IssueRegistrationToken(context.Background(), e.pool, e.now(), nil)
	if _, err := e.registerWith(reg2, "r1"); code(err) != connect.CodeAlreadyExists {
		t.Errorf("nome doppio: %v", err)
	}
	if _, err := e.registerWith(reg2, "nome non valido!"); code(err) != connect.CodeInvalidArgument {
		t.Errorf("nome non valido: %v", err)
	}
	if _, err := e.registerWith(reg2, "r2", "gpu"); err != nil {
		t.Errorf("il token non doveva essere consumato: %v", err)
	}

	// Nel database solo l'hash.
	var leaked int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM (
		SELECT row_to_json(t)::text AS s FROM core.ci_runners t
		UNION ALL SELECT row_to_json(t)::text FROM core.ci_runner_registration_tokens t) x
		WHERE position($1 in s) > 0 OR position($2 in s) > 0`, rt, reg).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("token in chiaro nel database: %d %v", leaked, err)
	}

	// r1 (self-hosted, linux) prende solo il job «linux».
	task, err := e.fetch(rt)
	if err != nil || task == nil {
		t.Fatalf("fetch: %v %v", task, err)
	}
	if got := task.GetContext().AsMap()["job"]; got != "linux" {
		t.Fatalf("job preso: %v", got)
	}
	taskToken(t, task) // stesso token del job del runner integrato
	if st, rn := e.jobStatus(runID, "linux"); st != "in_progress" || rn != "r1" {
		t.Errorf("linux: %s %q", st, rn)
	}
	// Un solo job per volta, e gli altri non sono suoi.
	if again, err := e.fetch(rt); err != nil || again != nil {
		t.Fatalf("secondo task mentre uno gira: %v %v", again, err)
	}
	if st, _ := e.jobStatus(runID, "gpu"); st != "queued" {
		t.Errorf("gpu: %s", st)
	}
	if st, _ := e.jobStatus(runID, "pod"); st != "queued" {
		t.Errorf("pod: %s", st)
	}
	// Chiuso il job, r1 non ha altro per sé (gpu non è sua, pod è interno).
	if err := e.update(taskToken(t, task), task.GetId(), runnerv1.Result_RESULT_SUCCESS); err != nil {
		t.Fatal(err)
	}
	if again, err := e.fetch(rt); err != nil || again != nil {
		t.Fatalf("r1 non deve prendere gpu né il pod: %v %v", again, err)
	}
}

// TestExternal_LogEStatiCometeIlRunnerIntegrato: un job su runner esterno pubblica log
// (con i segreti mascherati, anche il token del job) e stati dei passi; il run si chiude.
func TestExternal_LogEStatiCometeIlRunnerIntegrato(t *testing.T) {
	keys := testKeys(t)
	e := newEnvKeys(t, keys)
	ctx := context.Background()
	repo, runID := e.queueWF("app", "a", "name: x\non: push\njobs:\n  build:\n    runs-on: [self-hosted, linux]\n    steps:\n      - run: echo a\n      - run: echo b\n")
	if _, err := ci.PutSecret(ctx, e.pool, keys, e.now(), ci.Target{RepoID: repo}, uuid.New(), "DEPLOY_KEY", "segreto-esterno-42"); err != nil {
		t.Fatal(err)
	}
	rt, err := e.register("ext", "linux")
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.fetch(rt)
	if err != nil || task == nil {
		t.Fatalf("fetch: %v %v", task, err)
	}
	if task.GetSecrets()["DEPLOY_KEY"] != "segreto-esterno-42" {
		t.Errorf("segreti nel task: %v", keysOf(task.GetSecrets()))
	}
	tok := taskToken(t, task)
	// Il token del runner non fa UpdateLog/UpdateTask; quello del job non fa FetchTask.
	if err := e.update(rt, task.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("update col token del runner: %v", err)
	}
	if _, err := e.fetch(tok); code(err) != connect.CodeUnauthenticated {
		t.Errorf("fetch col token del job: %v", err)
	}

	row := func(s string) *runnerv1.LogRow { return &runnerv1.LogRow{Time: timestamppb.New(e.now()), Content: s} }
	if _, err := e.client.UpdateLog(ctx, authed(&runnerv1.UpdateLogRequest{TaskId: task.GetId(), Index: 0,
		Rows: []*runnerv1.LogRow{row("deploy con segreto-esterno-42"), row("token " + tok), row("riga innocua")}}, tok)); err != nil {
		t.Fatal(err)
	}
	t0 := timestamppb.New(e.now())
	if err := e.update(tok, task.GetId(), runnerv1.Result_RESULT_SUCCESS,
		&runnerv1.StepState{Id: 0, Result: runnerv1.Result_RESULT_SUCCESS, StartedAt: t0, StoppedAt: t0},
		&runnerv1.StepState{Id: 1, Result: runnerv1.Result_RESULT_SUCCESS, StartedAt: t0, StoppedAt: t0}); err != nil {
		t.Fatal(err)
	}

	jobID := e.jobID(runID, "build")
	size, err := e.logs.LogSize(ctx, e.pool, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := e.logs.ReadFrom(jobID, 0, size, &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if strings.Contains(out, "segreto-esterno-42") || strings.Contains(out, tok) || !strings.Contains(out, "riga innocua") {
		t.Errorf("log non mascherato:\n%s", out)
	}
	var jobStatus, jobConcl, runStatus string
	if err := e.pool.QueryRow(ctx, `SELECT j.status, j.conclusion, r.status FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id WHERE j.id = $1`, jobID).
		Scan(&jobStatus, &jobConcl, &runStatus); err != nil || jobStatus != "completed" || jobConcl != "success" || runStatus != "completed" {
		t.Fatalf("stati: %s %s %s %v", jobStatus, jobConcl, runStatus, err)
	}
	var steps int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_steps WHERE job_id = $1 AND conclusion = 'success'`, jobID).Scan(&steps); err != nil || steps != 2 {
		t.Errorf("passi: %d %v", steps, err)
	}
	// Il token del job è revocato a job chiuso, come per il runner integrato.
	if err := e.update(tok, task.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("token del job dopo la chiusura: %v", err)
	}
}

// TestExternal_RunnerRevocato: un runner revocato non prende più job (neppure uno già in
// attesa nel long-poll) e il job che stava eseguendo è chiuso come annullato.
func TestExternal_RunnerRevocato(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, runID := e.queueWF("app", "a", "name: x\non: push\njobs:\n  a:\n    runs-on: [self-hosted, linux]\n    steps:\n      - run: echo a\n  b:\n    runs-on: [self-hosted, linux]\n    steps:\n      - run: echo b\n")
	rt, err := e.register("ext", "linux")
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.fetch(rt)
	if err != nil || task == nil {
		t.Fatalf("fetch: %v %v", task, err)
	}
	tok := taskToken(t, task)
	runners, err := ci.ListRunners(ctx, e.pool, e.now())
	if err != nil || len(runners) != 1 || runners[0].Status != "busy" {
		t.Fatalf("elenco: %+v %v", runners, err)
	}
	if err := ci.DeleteRunner(ctx, e.pool, e.now(), runners[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := ci.DeleteRunner(ctx, e.pool, e.now(), runners[0].ID, nil); err == nil {
		t.Error("seconda revoca: atteso ErrRunnerNotFound")
	}
	if _, err := e.fetch(rt); code(err) != connect.CodeUnauthenticated {
		t.Fatalf("fetch dopo la revoca: %v", err)
	}
	// Il job che girava è annullato e il suo token non vale più.
	var st, concl string
	if err := e.pool.QueryRow(ctx, `SELECT status, conclusion FROM core.ci_jobs WHERE id = $1`, e.jobID(runID, "a")).Scan(&st, &concl); err != nil || concl != "cancelled" {
		t.Errorf("job del runner revocato: %s %s %v", st, concl, err)
	}
	if err := e.update(tok, task.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("token del job dopo la revoca: %v", err)
	}
	if st, _ := e.jobStatus(runID, "b"); st != "queued" {
		t.Errorf("b: %s", st)
	}
	// Un altro runner registrato dopo li prende.
	rt2, err := e.register("ext2", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if t2, err := e.fetch(rt2); err != nil || t2 == nil {
		t.Fatalf("nuovo runner: %v %v", t2, err)
	}
}

// TestExternal_Declare: il runner aggiorna etichette e versione col suo token; un token
// sbagliato è rifiutato.
func TestExternal_Declare(t *testing.T) {
	e := newEnv(t)
	e.queueWF("app", "a", externalWF)
	rt, err := e.register("ext", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.Declare(context.Background(), authed(&runnerv1.DeclareRequest{Version: "t2", Labels: []string{"gpu"}}, "gsr_sbagliato")); code(err) != connect.CodeUnauthenticated {
		t.Errorf("declare con token sbagliato: %v", err)
	}
	res, err := e.client.Declare(context.Background(), authed(&runnerv1.DeclareRequest{Version: "t2", Labels: []string{"GPU"}}, rt))
	if err != nil {
		t.Fatal(err)
	}
	if l := res.Msg.GetRunner().GetLabels(); len(l) != 2 || l[0] != "self-hosted" || l[1] != "gpu" {
		t.Errorf("etichette: %v", l)
	}
	task, err := e.fetch(rt)
	if err != nil || task == nil || task.GetContext().AsMap()["job"] != "gpu" {
		t.Fatalf("dopo Declare il runner prende gpu: %v %v", task, err)
	}
}
