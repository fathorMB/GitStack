//go:build integration

package cigateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const runnerSecret = "segreto-dei-runner-interni"

func sha(c string) string { return strings.Repeat(c, 40) }

// fakeSource è git per il motore e per il gateway: un workflow per sha.
type fakeSource struct{ files map[string][]ci.File }

func (f *fakeSource) Workflows(_ context.Context, _ uuid.UUID, s string) ([]ci.File, error) {
	return f.files[s], nil
}
func (f *fakeSource) Changed(context.Context, uuid.UUID, string, string) ([]string, error) {
	return nil, errors.New("sconosciuti")
}
func (f *fakeSource) CommitMessage(context.Context, uuid.UUID, string) (string, error) {
	return "", nil
}

// fakeReader registra ogni lettura di git chiesta dal gateway.
type fakeReader struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeReader) ReadJSON(context.Context, trust.Identity, uuid.UUID, string, url.Values) (json.RawMessage, error) {
	return nil, errors.New("non usata")
}

func (f *fakeReader) OpenStream(_ context.Context, _ trust.Identity, repo uuid.UUID, path string, q url.Values) (*gitclient.Stream, error) {
	f.mu.Lock()
	f.calls = append(f.calls, repo.String()+"|"+path+"|"+q.Get("ref")+"|"+q.Get("format"))
	f.mu.Unlock()
	return &gitclient.Stream{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("TARGZ:" + repo.String()))}, nil
}

type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	src    *fakeSource
	eng    *ci.Engine
	rd     *fakeReader
	srv    *httptest.Server
	client runnerv1connect.RunnerServiceClient
	clock  *atomic.Int64 // unix nanos
	logs   *ci.LogStore
}

func (e *env) now() time.Time { return time.Unix(0, e.clock.Load()).UTC() }
func (e *env) advance(d time.Duration) {
	e.clock.Add(int64(d))
}

func newEnv(t *testing.T) *env { return newEnvKeys(t, nil) }

func newEnvKeys(t *testing.T, keys ci.Sealer) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	e := &env{t: t, pool: pool, src: &fakeSource{files: map[string][]ci.File{}}, rd: &fakeReader{}, clock: &atomic.Int64{}}
	e.clock.Store(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).UnixNano())
	e.eng = &ci.Engine{Pool: pool, Source: e.src, Now: e.now}
	e.logs = &ci.LogStore{Dir: t.TempDir()}
	h := cigateway.New(cigateway.Config{
		Pool: pool, Source: e.src, Reader: e.rd, Logs: e.logs, RunnerSecrets: []string{"vecchio", runnerSecret},
		ArchiveURL: "http://ci-gateway.test", Keys: keys, Now: e.now, PollTimeout: 400 * time.Millisecond, PollEvery: 25 * time.Millisecond,
	})
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	e.client = runnerv1connect.NewRunnerServiceClient(e.srv.Client(), e.srv.URL)
	return e
}

func (e *env) repo(name string) uuid.UUID {
	e.t.Helper()
	id, owner := uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, owner.String()+"/"+name); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name, created_at) VALUES ($1, 'user', $2, 'alice', $3, $4)`,
		id, owner, name, e.now()); err != nil {
		e.t.Fatal(err)
	}
	return id
}

const twoJobs = `name: due
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo uno
      - run: echo due
  test:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: echo ok
`

// queue crea un repo con un push che mette in coda il workflow `twoJobs` a quello sha.
func (e *env) queue(name, commit string) (uuid.UUID, uuid.UUID) {
	e.t.Helper()
	repo := e.repo(name)
	e.src.files[sha(commit)] = []ci.File{{Path: ".github/workflows/ci.yml", Content: []byte(twoJobs)}}
	ids, err := e.eng.OnPush(context.Background(), ci.Push{RepoID: repo, Pusher: uuid.New(),
		Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("0"), After: sha(commit)}}})
	if err != nil || len(ids) != 1 {
		e.t.Fatalf("push: %v %v", ids, err)
	}
	return repo, ids[0]
}

func authed[T any](msg *T, token string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set(cigateway.HeaderRunnerToken, token)
	return r
}

func (e *env) fetch(token string) (*runnerv1.Task, error) {
	res, err := e.client.FetchTask(context.Background(), authed(&runnerv1.FetchTaskRequest{}, token))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

func code(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return connect.Code(0)
}

func (e *env) update(token string, taskID int64, result runnerv1.Result, steps ...*runnerv1.StepState) error {
	_, err := e.client.UpdateTask(context.Background(), authed(&runnerv1.UpdateTaskRequest{
		State: &runnerv1.TaskState{Id: taskID, Result: result, Steps: steps}}, token))
	return err
}

func taskToken(t *testing.T, task *runnerv1.Task) string {
	t.Helper()
	tok := task.GetSecrets()["GITHUB_TOKEN"]
	if !strings.HasPrefix(tok, ci.JobTokenPrefix) {
		t.Fatalf("token del job assente: %q", tok)
	}
	return tok
}

// TestFetchTask_UnJobUnaVoltaSola: due runner in parallelo su un solo job in coda,
// ne prende uno solo; il task porta workflow, contesto e il token del job.
func TestFetchTask_UnJobUnaVoltaSola(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queue("app", "a")

	var wg sync.WaitGroup
	tasks := make([]*runnerv1.Task, 2)
	errs := make([]error, 2)
	for i := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tasks[i], errs[i] = e.fetch(runnerSecret)
		}()
	}
	wg.Wait()
	got := 0
	var task *runnerv1.Task
	for i := range tasks {
		if errs[i] != nil {
			t.Fatalf("runner %d: %v", i, errs[i])
		}
		if tasks[i] != nil {
			got++
			task = tasks[i]
		}
	}
	if got != 1 {
		t.Fatalf("task consegnati: %d, atteso 1", got)
	}
	if string(task.GetWorkflowPayload()) != twoJobs {
		t.Errorf("payload: %q", task.GetWorkflowPayload())
	}
	cx := task.GetContext().AsMap()
	if cx["job"] != "build" || cx["sha"] != sha("a") || cx["repository"] != "alice/app" || cx["gitstack_archive_url"] != "http://ci-gateway.test"+cigateway.ArchivePath {
		t.Errorf("contesto: %v", cx)
	}
	if cx["token"] != taskToken(t, task) {
		t.Error("il token del contesto e GITHUB_TOKEN dovrebbero coincidere")
	}
	run, _ := ci.GetRun(context.Background(), e.pool, mustRepo(t, e, runID), runID)
	if run.Status != "in_progress" {
		t.Errorf("run: %s", run.Status)
	}
	// Il secondo job aspetta `build`: nessuno lo riceve finché il primo non finisce.
	if again, err := e.fetch(runnerSecret); err != nil || again != nil {
		t.Fatalf("job con needs consegnato: %v %v", again, err)
	}
}

func mustRepo(t *testing.T, e *env, runID uuid.UUID) uuid.UUID {
	t.Helper()
	var repo uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT repo_id FROM core.ci_runs WHERE id = $1`, runID).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestFetchTask_Credenziale: senza il segreto dei runner, o con il token di un job, FetchTask è rifiutato.
func TestFetchTask_Credenziale(t *testing.T) {
	e := newEnv(t)
	e.queue("app", "a")
	for _, tok := range []string{"", "sbagliato", ci.JobTokenPrefix + "xxx"} {
		if _, err := e.fetch(tok); code(err) != connect.CodeUnauthenticated {
			t.Errorf("token %q: %v", tok, err)
		}
	}
	// Il segreto vecchio (rotazione) vale ancora.
	if task, err := e.fetch("vecchio"); err != nil || task == nil {
		t.Fatalf("segreto in rotazione: %v %v", task, err)
	}
	// Register e Declare sono di GIT-278.
	_, err := e.client.Register(context.Background(), authed(&runnerv1.RegisterRequest{Name: "x"}, runnerSecret))
	if code(err) != connect.CodeUnimplemented {
		t.Errorf("register: %v", err)
	}
}

// TestUpdateTask_ConcludeIlJobEIlRunAvanza: l'esito chiude il job come CompleteJob,
// promuove il job con needs e, finiti tutti, chiude il run.
func TestUpdateTask_ConcludeIlJobEIlRunAvanza(t *testing.T) {
	e := newEnv(t)
	repo, runID := e.queue("app", "a")
	ctx := context.Background()

	t1, err := e.fetch(runnerSecret)
	if err != nil || t1 == nil {
		t.Fatalf("fetch: %v %v", t1, err)
	}
	tok1 := taskToken(t, t1)
	start := timestamppb.New(e.now())
	if err := e.update(tok1, t1.GetId(), runnerv1.Result_RESULT_UNSPECIFIED,
		&runnerv1.StepState{Id: 0, Result: runnerv1.Result_RESULT_SUCCESS, StartedAt: start, StoppedAt: start},
		&runnerv1.StepState{Id: 1, StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	run, _ := ci.GetRun(ctx, e.pool, repo, runID)
	if run.Jobs[0].Status != "in_progress" {
		t.Fatalf("job: %s", run.Jobs[0].Status)
	}
	var s1, s2 string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM core.ci_steps WHERE job_id = $1 AND number = 1`, run.Jobs[0].ID).Scan(&s1); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT status FROM core.ci_steps WHERE job_id = $1 AND number = 2`, run.Jobs[0].ID).Scan(&s2); err != nil {
		t.Fatal(err)
	}
	if s1 != "completed" || s2 != "in_progress" {
		t.Fatalf("passi: %s %s", s1, s2)
	}
	if err := e.update(tok1, t1.GetId(), runnerv1.Result_RESULT_SUCCESS); err != nil {
		t.Fatal(err)
	}
	run, _ = ci.GetRun(ctx, e.pool, repo, runID)
	if run.Jobs[0].Status != "completed" || *run.Jobs[0].Conclusion != "success" || run.Jobs[1].Status != "queued" {
		t.Fatalf("dopo build: %+v", run.Jobs)
	}
	// Il token del primo job è revocato alla fine del job.
	if err := e.update(tok1, t1.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("token dopo la fine: %v", err)
	}

	t2, err := e.fetch(runnerSecret)
	if err != nil || t2 == nil || t2.GetContext().AsMap()["job"] != "test" {
		t.Fatalf("secondo job: %v %v", t2, err)
	}
	if err := e.update(taskToken(t, t2), t2.GetId(), runnerv1.Result_RESULT_FAILURE); err != nil {
		t.Fatal(err)
	}
	run, _ = ci.GetRun(ctx, e.pool, repo, runID)
	if run.Status != "completed" || run.Conclusion == nil || *run.Conclusion != "failure" {
		t.Fatalf("run: %s %v", run.Status, run.Conclusion)
	}
}

// TestToken_ScadutoRevocatoOAltroJob: un token scaduto, revocato o di un altro job è rifiutato.
func TestToken_ScadutoRevocatoOAltroJob(t *testing.T) {
	e := newEnv(t)
	e.queue("uno", "a")
	e.queue("due", "b")
	ta, err := e.fetch(runnerSecret)
	if err != nil || ta == nil {
		t.Fatal(ta, err)
	}
	tb, err := e.fetch(runnerSecret)
	if err != nil || tb == nil {
		t.Fatal(tb, err)
	}
	tokA, tokB := taskToken(t, ta), taskToken(t, tb)
	if tokA == tokB {
		t.Fatal("token uguali")
	}

	// Di un altro job: il token di B non scrive sul task di A, né i suoi log.
	if err := e.update(tokB, ta.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodePermissionDenied {
		t.Errorf("task altrui: %v", err)
	}
	_, err = e.client.UpdateLog(context.Background(), authed(&runnerv1.UpdateLogRequest{TaskId: ta.GetId(), Rows: []*runnerv1.LogRow{{Content: "x"}}}, tokB))
	if code(err) != connect.CodePermissionDenied {
		t.Errorf("log altrui: %v", err)
	}

	// Revocato: B chiuso.
	if err := e.update(tokB, tb.GetId(), runnerv1.Result_RESULT_SUCCESS); err != nil {
		t.Fatal(err)
	}
	if err := e.update(tokB, tb.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("revocato: %v", err)
	}
	// Sconosciuto.
	if err := e.update(ci.JobTokenPrefix+"sconosciuto", ta.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("sconosciuto: %v", err)
	}

	// Scaduto: oltre tetto di durata + 5 minuti; subito prima vale ancora.
	e.advance(ci.JobMaxDuration + ci.JobTokenGrace - time.Second)
	if err := e.update(tokA, ta.GetId(), runnerv1.Result_RESULT_UNSPECIFIED); err != nil {
		t.Fatalf("prima della scadenza: %v", err)
	}
	e.advance(2 * time.Second)
	if err := e.update(tokA, ta.GetId(), runnerv1.Result_RESULT_SUCCESS); code(err) != connect.CodeUnauthenticated {
		t.Errorf("scaduto: %v", err)
	}
	if st := e.archive(tokA, ""); st != http.StatusUnauthorized {
		t.Errorf("archivio con token scaduto: %d", st)
	}
}

func (e *env) archive(token, query string) int {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+cigateway.ArchivePath+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

// TestToken_ClonaSoloIlRepoDelJob: il token dà l'archivio del repo del job a quello sha
// e nient'altro: nessun parametro sceglie un altro repo, nessun altro percorso risponde.
func TestToken_ClonaSoloIlRepoDelJob(t *testing.T) {
	e := newEnv(t)
	repoA, _ := e.queue("uno", "a")
	repoB, _ := e.queue("due", "b")
	ta, err := e.fetch(runnerSecret)
	if err != nil || ta == nil {
		t.Fatal(ta, err)
	}
	if ta.GetContext().AsMap()["repository"] != "alice/uno" {
		t.Fatalf("il primo job dovrebbe essere di uno: %v", ta.GetContext().AsMap())
	}
	tok := taskToken(t, ta)

	req, _ := http.NewRequest("GET", e.srv.URL+cigateway.ArchivePath, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "TARGZ:"+repoA.String() {
		t.Fatalf("archivio: %d %q", res.StatusCode, body)
	}

	// Un altro repo: provare a sceglierlo (parametri, percorsi) non cambia il repo letto.
	for _, q := range []string{"?repo=" + repoB.String(), "?ref=" + sha("b"), "?repoId=" + repoB.String() + "&format=zip"} {
		if st := e.archive(tok, q); st != 200 {
			t.Fatalf("archivio %s: %d", q, st)
		}
	}
	e.rd.mu.Lock()
	calls := append([]string(nil), e.rd.calls...)
	e.rd.mu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("letture di git: %v", calls)
	}
	for _, c := range calls {
		if c != repoA.String()+"|archive|"+sha("a")+"|tar.gz" {
			t.Errorf("lettura fuori dal repo del job: %s", c)
		}
	}

	// Nessun'altra API: endpoint di scrittura, letture, altri repo e l'RPC di fetch.
	do := func(method, path string) int {
		req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set(cigateway.HeaderRunnerToken, tok)
		res, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	for _, c := range []struct{ m, p string }{
		{"POST", "/repos/alice/uno/issues"},
		{"PUT", "/repos/alice/uno/contents/x"},
		{"POST", "/repos/alice/due/actions/runs/" + uuid.NewString() + "/cancel"},
		{"GET", "/repos/alice/due"},
		{"GET", "/resources"},
		{"POST", "/ci/v1/archive"},
		{"GET", "/ci/v1/other"},
		{"GET", "/" + repoB.String() + ".git/info/refs"},
	} {
		if st := do(c.m, c.p); st != http.StatusNotFound && st != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d, atteso 404/405", c.m, c.p, st)
		}
	}
	// FetchTask con il token del job: è un'altra credenziale.
	if _, err := e.fetch(tok); code(err) != connect.CodeUnauthenticated {
		t.Errorf("fetch con token del job: %v", err)
	}
	// Senza token: 401.
	if st := e.archive("", ""); st != http.StatusUnauthorized {
		t.Errorf("senza token: %d", st)
	}
}

// TestUpdateLog_RigheInOrdineEIdempotenza: le righe si scrivono in ordine, un reinvio
// non le duplica, un buco fa rimandare dal punto giusto.
func TestUpdateLog_RigheInOrdineEIdempotenza(t *testing.T) {
	e := newEnv(t)
	repo, runID := e.queue("app", "a")
	task, err := e.fetch(runnerSecret)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	tok := taskToken(t, task)
	row := func(s string) *runnerv1.LogRow {
		return &runnerv1.LogRow{Time: timestamppb.New(e.now()), Content: s}
	}
	send := func(index int64, rows ...*runnerv1.LogRow) int64 {
		t.Helper()
		res, err := e.client.UpdateLog(context.Background(), authed(&runnerv1.UpdateLogRequest{TaskId: task.GetId(), Index: index, Rows: rows}, tok))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetAckIndex()
	}
	if ack := send(0, row("uno"), row("due\nsu due righe")); ack != 2 {
		t.Fatalf("ack: %d", ack)
	}
	if ack := send(1, row("due\nsu due righe"), row("tre")); ack != 3 { // reinvio parziale
		t.Fatalf("ack dopo reinvio: %d", ack)
	}
	if ack := send(0, row("uno"), row("due")); ack != 3 { // reinvio intero già scritto
		t.Fatalf("ack dopo reinvio completo: %d", ack)
	}
	if ack := send(7, row("buco")); ack != 3 {
		t.Fatalf("ack con buco: %d", ack)
	}
	if ack := send(3, row("quattro")); ack != 4 {
		t.Fatalf("ack: %d", ack)
	}
	job, err := ci.JobRef(context.Background(), e.pool, repo, e.jobID(runID, "build"))
	if err != nil {
		t.Fatal(err)
	}
	size, err := e.logs.LogSize(context.Background(), e.pool, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if _, err := e.logs.ReadFrom(job.ID, 0, size, &sb); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	want := []string{"uno", "due su due righe", "tre", "quattro"}
	if len(lines) != len(want) {
		t.Fatalf("righe: %q", lines)
	}
	for i, l := range lines {
		if !strings.HasSuffix(l, " "+want[i]) || !strings.HasPrefix(l, "2026-10-20T10:00:00.000Z ") {
			t.Errorf("riga %d: %q", i, l)
		}
	}
}

// TestUpdateLog_LimiteDiDimensione: oltre il limite il log si tronca con una riga di avviso.
func TestUpdateLog_LimiteDiDimensione(t *testing.T) {
	e := newEnv(t)
	_, runID := e.queue("app", "a")
	e.logs.MaxBytes = 300
	task, err := e.fetch(runnerSecret)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	var rows []*runnerv1.LogRow
	for i := 0; i < 20; i++ {
		rows = append(rows, &runnerv1.LogRow{Time: timestamppb.New(e.now()), Content: strings.Repeat("x", 40)})
	}
	res, err := e.client.UpdateLog(context.Background(), authed(&runnerv1.UpdateLogRequest{TaskId: task.GetId(), Rows: rows}, taskToken(t, task)))
	if err != nil || res.Msg.GetAckIndex() != 20 {
		t.Fatalf("ack: %v %v", res, err)
	}
	id := e.jobID(runID, "build")
	size, _ := e.logs.LogSize(context.Background(), e.pool, id)
	var sb strings.Builder
	_, _ = e.logs.ReadFrom(id, 0, size, &sb)
	if size > 400 || !strings.Contains(sb.String(), "log troncato") {
		t.Fatalf("size %d: %q", size, sb.String())
	}
}

func (e *env) jobID(runID uuid.UUID, key string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.ci_jobs WHERE run_id = $1 AND job_key = $2`, runID, key).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}
