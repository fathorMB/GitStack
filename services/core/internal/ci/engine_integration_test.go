//go:build integration

package ci_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakePods struct {
	mu         sync.Mutex
	created    []ci.Pod
	exists     bool
	conclusion string
}

func (f *fakePods) Create(_ context.Context, p ci.Pod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, p)
	return nil
}

func (f *fakePods) Wait(_ context.Context, _, _ string) (string, error) {
	return f.conclusion, nil
}

func (f *fakePods) Exists(_ context.Context, _, _ string) (bool, error) { return f.exists, nil }

type captureReporter struct {
	mu      sync.Mutex
	changes []ci.Change
}

func (r *captureReporter) Changed(_ context.Context, _ pgx.Tx, change ci.Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, change)
	return nil
}

func waitJobConclusion(t *testing.T, e *env, id uuid.UUID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got *string
		err := e.pool.QueryRow(context.Background(), `SELECT conclusion FROM core.ci_jobs WHERE id = $1`, id).Scan(&got)
		if err == nil && got != nil && *got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s non chiuso con %q", id, want)
}

func queuedInternalJob(t *testing.T, e *env) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	repo := e.repo(uuid.New(), "runner")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	ids, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("accodamento: %v %v", ids, err)
	}
	var jobID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM core.ci_jobs WHERE run_id = $1`, ids[0]).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}

// fakeSource è git: i workflow dipendono dallo sha, i file cambiati dalla coppia base..head.
type fakeSource struct {
	mu       sync.Mutex
	files    map[string][]ci.File // sha -> workflow
	changed  map[string][]string  // "base..head" -> file
	messages map[string]string
	reads    int
}

func newSource() *fakeSource {
	return &fakeSource{files: map[string][]ci.File{}, changed: map[string][]string{}, messages: map[string]string{}}
}

func (f *fakeSource) Workflows(_ context.Context, _ uuid.UUID, sha string) ([]ci.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.files[sha], nil
}

func (f *fakeSource) Changed(_ context.Context, _ uuid.UUID, base, head string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.changed[base+".."+head]
	if !ok {
		return nil, errors.New("sconosciuti")
	}
	return c, nil
}

func (f *fakeSource) CommitMessage(_ context.Context, _ uuid.UUID, sha string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages[sha], nil
}

func sha(c string) string { return strings.Repeat(c, 40) }

const body = "\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"

func wfFile(p, on string) ci.File {
	return ci.File{Path: p, Content: []byte("name: " + p + "\non:\n" + on + body)}
}

type env struct {
	t     *testing.T
	pool  *pgxpool.Pool
	src   *fakeSource
	eng   *ci.Engine
	clock *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	e := &env{t: t, pool: pool, src: newSource(), clock: &now}
	e.eng = &ci.Engine{Pool: pool, Source: e.src, Now: func() time.Time { return *e.clock }}
	return e
}

func (e *env) advance(d time.Duration) { *e.clock = e.clock.Add(d) }

// repo crea un repo dell'owner indicato; created = quando è stato creato.
func (e *env) repo(owner uuid.UUID, name string) uuid.UUID {
	e.t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, owner.String()+"/"+name); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, created_at) VALUES ($1, 'user', $2, $3, $4)`,
		id, owner, name, *e.clock); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) runs(repo uuid.UUID) []ci.RunRow {
	e.t.Helper()
	r, err := ci.ListRuns(context.Background(), e.pool, repo, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) count(repo uuid.UUID, status string) int {
	n := 0
	for _, r := range e.runs(repo) {
		if r.Status == status {
			n++
		}
	}
	return n
}

func pushOf(repo uuid.UUID, pusher uuid.UUID, ref, before, after, msg string) ci.Push {
	return ci.Push{RepoID: repo, Pusher: pusher, Refs: []ci.PushRef{{Ref: ref, Before: before, After: after, Message: msg}}}
}

func TestPush_CreaLeEsecuzioni(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, pusher := uuid.New(), uuid.New()
	repo := e.repo(owner, "r")
	e.src.files[sha("a")] = []ci.File{
		wfFile(".github/workflows/ci.yml", "  push:\n    branches: [main]"),
		wfFile(".gitstack/workflows/docs.yml", "  push:\n    paths: ['docs/**']"),
		wfFile(".github/workflows/release.yml", "  push:\n    tags: ['v*']"),
		wfFile(".github/workflows/manual.yml", "  workflow_dispatch:"),
	}
	e.src.changed[sha("9")+".."+sha("a")] = []string{"main.go"}

	ids, err := e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/heads/main", sha("9"), sha("a"), "feat: x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("attesa 1 esecuzione (ci.yml; docs.yml per i path no; release per i tag no), create %d", len(ids))
	}
	run, err := ci.GetRun(ctx, e.pool, repo, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if run.Event != "push" || run.Ref != "refs/heads/main" || run.SHA != sha("a") || run.Status != "queued" ||
		run.ActorID == nil || *run.ActorID != pusher || run.WorkflowPath != ".github/workflows/ci.yml" {
		t.Errorf("run: %+v", run)
	}
	if len(run.Jobs) != 1 || run.Jobs[0].Status != "queued" || run.Jobs[0].RunnerType != "internal_pod" ||
		run.Jobs[0].Permissions["contents"] != "read" {
		t.Errorf("job: %+v", run.Jobs)
	}

	// Il tag avvia solo il workflow dei tag.
	e.src.files[sha("b")] = e.src.files[sha("a")]
	ids, err = e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/tags/v1.0", sha("9"), sha("b"), ""))
	if err != nil || len(ids) != 1 {
		t.Fatalf("tag: %v %v", ids, err)
	}
	// I paths: un push che tocca docs/ avvia docs.yml.
	e.src.changed[sha("a")+".."+sha("b")] = []string{"docs/x.md"}
	ids, err = e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/heads/feature", sha("a"), sha("b"), ""))
	if err != nil || len(ids) != 1 {
		t.Fatalf("paths: %v %v", ids, err)
	}
	r, _ := ci.GetRun(ctx, e.pool, repo, ids[0])
	if r.WorkflowPath != ".gitstack/workflows/docs.yml" {
		t.Errorf("workflow %s", r.WorkflowPath)
	}
	// Eliminazione del branch e ref che non è branch né tag: niente.
	for _, p := range []ci.Push{
		pushOf(repo, pusher, "refs/heads/main", sha("a"), ci.ZeroSHA, ""),
		pushOf(repo, pusher, "refs/notes/x", sha("a"), sha("b"), ""),
	} {
		if ids, err := e.eng.OnPush(ctx, p); err != nil || len(ids) != 0 {
			t.Errorf("nessuna esecuzione attesa: %v %v", ids, err)
		}
	}
}

func TestPush_SkipCI(t *testing.T) {
	e := newEnv(t)
	repo := e.repo(uuid.New(), "r")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	ids, err := e.eng.OnPush(context.Background(), pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "docs: typo [skip ci]"))
	if err != nil || len(ids) != 0 {
		t.Fatalf("[skip ci] nel messaggio: %v %v", ids, err)
	}
	// Il messaggio, se non è nel push, si legge da git.
	e.src.files[sha("b")] = e.src.files[sha("a")]
	e.src.messages[sha("b")] = "fix\n\n[ci skip]"
	ids, err = e.eng.OnPush(context.Background(), pushOf(repo, uuid.New(), "refs/heads/main", sha("a"), sha("b"), ""))
	if err != nil || len(ids) != 0 {
		t.Fatalf("[ci skip] letto da git: %v %v", ids, err)
	}
	// Senza la parola chiave parte.
	e.src.files[sha("c")] = e.src.files[sha("a")]
	ids, err = e.eng.OnPush(context.Background(), pushOf(repo, uuid.New(), "refs/heads/main", sha("b"), sha("c"), "ok"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("senza skip: %v %v", ids, err)
	}
}

func TestPush_RepoArchiviatoEliminatoEMirrorInPull(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:\n  workflow_dispatch:")}
	push := func(repo uuid.UUID, n string) int {
		t.Helper()
		e.src.files[sha(n)] = e.src.files[sha("a")]
		ids, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha(n), "x"))
		if err != nil {
			t.Fatal(err)
		}
		return len(ids)
	}

	archived := e.repo(uuid.New(), "archived")
	e.exec(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, archived)
	if n := push(archived, "b"); n != 0 {
		t.Errorf("repo archiviato: %d esecuzioni", n)
	}
	if _, err := e.eng.Dispatch(ctx, ci.Dispatch{RepoID: archived, Workflow: "ci.yml", Ref: "refs/heads/main", SHA: sha("a"), Actor: uuid.New()}); !errors.Is(err, ci.ErrRepoDisabled) {
		t.Errorf("dispatch su archiviato: %v", err)
	}
	// Disarchiviato torna a funzionare.
	e.exec(`UPDATE core.repositories SET archived_at = NULL WHERE resource_id = $1`, archived)
	if n := push(archived, "c"); n != 1 {
		t.Errorf("repo disarchiviato: %d", n)
	}

	deleted := e.repo(uuid.New(), "deleted")
	e.exec(`UPDATE core.repositories SET deleted_at = now() WHERE resource_id = $1`, deleted)
	if n := push(deleted, "d"); n != 0 {
		t.Errorf("repo eliminato: %d", n)
	}

	mirror := e.repo(uuid.New(), "mirror")
	e.exec(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, 'https://example.org/o/r.git', $3)`,
		uuid.New(), mirror, uuid.New())
	if n := push(mirror, "e"); n != 0 {
		t.Errorf("mirror in pull, spenta di default: %d", n)
	}
	if _, err := e.eng.Dispatch(ctx, ci.Dispatch{RepoID: mirror, Workflow: "ci.yml", Ref: "refs/heads/main", SHA: sha("a"), Actor: uuid.New()}); !errors.Is(err, ci.ErrRepoDisabled) {
		t.Errorf("dispatch su mirror in pull: %v", err)
	}
	// Accesa per quel repo, parte.
	e.exec(`INSERT INTO core.ci_repo_state (repo_id, run_on_pull_mirror) VALUES ($1, true) ON CONFLICT (repo_id) DO UPDATE SET run_on_pull_mirror = true`, mirror)
	if n := push(mirror, "f"); n != 1 {
		t.Errorf("mirror in pull acceso: %d", n)
	}
	// Un mirror interrotto è un repo normale (MP stopped), anche con l'interruttore spento.
	e.exec(`UPDATE core.ci_repo_state SET run_on_pull_mirror = false WHERE repo_id = $1`, mirror)
	e.exec(`UPDATE core.repo_pull_mirrors SET state = 'stopped', stopped_at = now() WHERE repo_id = $1`, mirror)
	if n := push(mirror, "9"); n != 1 {
		t.Errorf("mirror interrotto: %d", n)
	}
}

func TestPush_ConIlTokenDelJobNonAvviaWorkflow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	ids, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("push normale: %v %v", ids, err)
	}
	// Il job ha un'identità per il suo token: un push con quell'identità non avvia nulla.
	principal := uuid.New()
	e.exec(`UPDATE core.ci_jobs SET token_principal_id = $1 WHERE run_id = $2`, principal, ids[0])
	e.src.files[sha("b")] = e.src.files[sha("a")]
	ids, err = e.eng.OnPush(ctx, pushOf(repo, principal, "refs/heads/main", sha("a"), sha("b"), "chore: bump"))
	if err != nil || len(ids) != 0 {
		t.Fatalf("push del token del job: %v %v", ids, err)
	}
	if n := len(e.runs(repo)); n != 1 {
		t.Errorf("esecuzioni %d, attesa solo la prima", n)
	}
	// E non conta come push che tiene vivo il repo.
	var last *time.Time
	if err := e.pool.QueryRow(ctx, `SELECT last_push_at FROM core.ci_repo_state WHERE repo_id = $1`, repo).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last == nil || !last.Equal(*e.clock) {
		t.Errorf("last_push_at %v", last)
	}
	// Anche una PR aperta con quell'identità non avvia nulla.
	e.src.files[sha("c")] = []ci.File{wfFile(".github/workflows/pr.yml", "  pull_request:")}
	ids, err = e.eng.OnPull(ctx, ci.Pull{RepoID: repo, Number: 1, Action: "opened", SourceBranch: "bot", TargetBranch: "main", HeadSHA: sha("c"), Actor: principal})
	if err != nil || len(ids) != 0 {
		t.Fatalf("PR del token del job: %v %v", ids, err)
	}
}

func TestPull_EventiEFiltri(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, actor := uuid.New(), uuid.New()
	repo := e.repo(owner, "r")
	e.src.files[sha("a")] = []ci.File{
		wfFile(".github/workflows/pr.yml", "  pull_request:\n    branches: [main]\n    paths: ['api/**']"),
		wfFile(".github/workflows/closed.yml", "  pull_request:\n    types: [closed]"),
	}
	e.src.changed["main.."+sha("a")] = []string{"api/openapi.yaml"}
	pr := ci.Pull{RepoID: repo, Number: 7, Action: "opened", SourceBranch: "feat", TargetBranch: "main", HeadSHA: sha("a"), Actor: actor}
	ids, err := e.eng.OnPull(ctx, pr)
	if err != nil || len(ids) != 1 {
		t.Fatalf("PR aperta: %v %v", ids, err)
	}
	run, _ := ci.GetRun(ctx, e.pool, repo, ids[0])
	if run.Event != "pull_request" || run.Ref != "refs/pull/7/head" || run.PullNumber == nil || *run.PullNumber != 7 || run.SHA != sha("a") {
		t.Errorf("run %+v", run)
	}
	// Verso un altro branch: non parte.
	other := pr
	other.Number, other.TargetBranch = 8, "develop"
	e.src.changed["develop.."+sha("a")] = []string{"api/openapi.yaml"}
	if ids, err := e.eng.OnPull(ctx, other); err != nil || len(ids) != 0 {
		t.Errorf("destinazione develop: %v %v", ids, err)
	}
	// Senza file in api/ non parte.
	e.src.changed["main.."+sha("a")] = []string{"web/a.ts"}
	pr2 := pr
	pr2.Number = 9
	if ids, err := e.eng.OnPull(ctx, pr2); err != nil || len(ids) != 0 {
		t.Errorf("paths: %v %v", ids, err)
	}
	// [skip ci] nel commit di testa.
	e.src.changed["main.."+sha("a")] = []string{"api/x"}
	e.src.messages[sha("a")] = "wip [skip ci]"
	pr3 := pr
	pr3.Number = 10
	if ids, err := e.eng.OnPull(ctx, pr3); err != nil || len(ids) != 0 {
		t.Errorf("skip ci: %v %v", ids, err)
	}
	e.src.messages[sha("a")] = ""
	// Archiviato.
	e.exec(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repo)
	pr4 := pr
	pr4.Number = 11
	if ids, err := e.eng.OnPull(ctx, pr4); err != nil || len(ids) != 0 {
		t.Errorf("repo archiviato: %v %v", ids, err)
	}
}

func TestCoda_UnPushNuovoAnnullaIlRunInCodaDelloStessoBranch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	pusher := uuid.New()
	for _, n := range []string{"a", "b", "c", "d"} {
		e.src.files[sha(n)] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	}
	first, _ := e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/heads/main", sha("9"), sha("a"), "1"))
	other, _ := e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/heads/dev", sha("9"), sha("a"), "1"))
	e.advance(time.Minute)
	second, err := e.eng.OnPush(ctx, pushOf(repo, pusher, "refs/heads/main", sha("a"), sha("b"), "2"))
	if err != nil || len(second) != 1 {
		t.Fatal(err, second)
	}
	r1, _ := ci.GetRun(ctx, e.pool, repo, first[0])
	if r1.Status != "cancelled" || r1.Conclusion == nil || *r1.Conclusion != "cancelled" || r1.CompletedAt == nil {
		t.Errorf("il run in coda doveva essere annullato: %+v", r1)
	}
	for _, j := range r1.Jobs {
		if j.Status != "cancelled" {
			t.Errorf("job non annullato: %+v", j)
		}
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, other[0]); r.Status != "queued" {
		t.Errorf("un altro branch non va annullato: %s", r.Status)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, second[0]); r.Status != "queued" {
		t.Errorf("il nuovo run resta in coda: %s", r.Status)
	}

}

func TestCoda_UnPushNuovoNonAnnullaUnRunGiaPartito(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	for _, n := range []string{"a", "b"} {
		e.src.files[sha(n)] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	}
	first, _ := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "1"))
	if _, _, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 3, "pod-1"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	second, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("a"), sha("b"), "2"))
	if err != nil || len(second) != 1 {
		t.Fatal(err, second)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, first[0]); r.Status != "in_progress" {
		t.Errorf("run in corso: %s", r.Status)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, second[0]); r.Status != "queued" {
		t.Errorf("run nuovo: %s", r.Status)
	}
}

func TestCoda_UnaPRNuovaSostituisceIlRunInCodaDellaStessaPR(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	actor := uuid.New()
	for _, n := range []string{"a", "b"} {
		e.src.files[sha(n)] = []ci.File{wfFile(".github/workflows/pr.yml", "  pull_request:")}
		e.src.changed["main.."+sha(n)] = []string{"x"}
	}
	mk := func(n int64, head, action string) []uuid.UUID {
		ids, err := e.eng.OnPull(ctx, ci.Pull{RepoID: repo, Number: n, Action: action, SourceBranch: "f", TargetBranch: "main", HeadSHA: head, Actor: actor})
		if err != nil || len(ids) != 1 {
			t.Fatalf("PR %d: %v %v", n, ids, err)
		}
		return ids
	}
	pr1a := mk(1, sha("a"), "opened")
	pr2 := mk(2, sha("a"), "opened")
	pr1b := mk(1, sha("b"), "synchronize")
	if r, _ := ci.GetRun(ctx, e.pool, repo, pr1a[0]); r.Status != "cancelled" {
		t.Errorf("il run della PR 1 superato dal synchronize: %s", r.Status)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, pr2[0]); r.Status != "queued" {
		t.Errorf("la PR 2 non va toccata: %s", r.Status)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, pr1b[0]); r.Status != "queued" {
		t.Errorf("nuovo run: %s", r.Status)
	}
	if n := e.count(repo, "queued"); n != 2 {
		t.Errorf("in coda %d", n)
	}
}

func TestCoda_LimitePerOwner(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	ra, rb := e.repo(alice, "a"), e.repo(bob, "b")
	// 4 posti: al massimo 2 per owner. Alice accoda 4 job (4 branch), Bob 1, in quest'ordine.
	if ci.OwnerLimit(4) != 2 || ci.OwnerLimit(3) != 1 || ci.OwnerLimit(1) != 1 || ci.OwnerLimit(6) != 3 {
		t.Fatal("OwnerLimit")
	}
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	for _, b := range []string{"b1", "b2", "b3", "b4"} {
		e.advance(time.Second)
		if ids, err := e.eng.OnPush(ctx, pushOf(ra, uuid.New(), "refs/heads/"+b, sha("9"), sha("a"), "x")); err != nil || len(ids) != 1 {
			t.Fatal(ids, err)
		}
	}
	e.advance(time.Second)
	if ids, err := e.eng.OnPush(ctx, pushOf(rb, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x")); err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}

	claim := func() (ci.RunRow, bool) {
		_, run, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 4, "pod")
		if err != nil {
			t.Fatal(err)
		}
		return run, ok
	}
	owners := map[uuid.UUID]int{}
	var order []uuid.UUID
	for i := 0; i < 3; i++ {
		run, ok := claim()
		if !ok {
			t.Fatalf("claim %d: nulla in coda", i)
		}
		owners[run.RepoID]++
		order = append(order, run.RepoID)
	}
	// Equa: Alice ne prende uno, poi tocca a Bob (meno job in corso), poi Alice fino al suo tetto.
	if owners[ra] != 2 || owners[rb] != 1 {
		t.Fatalf("job in corso: alice %d (atteso 2), bob %d (atteso 1)", owners[ra], owners[rb])
	}
	if order[0] != ra || order[1] != rb {
		t.Errorf("ordine non equo: %v", order)
	}
	// Alice ha 2 in corso (il tetto): il quarto posto libero non va a un terzo job di Alice.
	if run, ok := claim(); ok {
		t.Fatalf("Alice è al tetto di %d job: non doveva partire altro, partito %v", ci.OwnerLimit(4), run.RepoID)
	}
	var running int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_jobs WHERE status = 'in_progress'`).Scan(&running); err != nil || running != 3 {
		t.Fatalf("in corso %d (%v)", running, err)
	}
	// Quando un job di Alice finisce, ne parte un altro di Alice.
	var jobID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT j.id FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id
		WHERE j.status = 'in_progress' AND r.repo_id = $1 LIMIT 1`, ra).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if err := ci.CompleteJob(ctx, e.pool, *e.clock, jobID, "success"); err != nil {
		t.Fatal(err)
	}
	if run, ok := claim(); !ok || run.RepoID != ra {
		t.Fatalf("dopo il completamento doveva partire un job di Alice: %v %v", ok, run.RepoID)
	}
	// Alice è di nuovo al tetto: niente altro, anche se resta un posto libero.
	if _, ok := claim(); ok {
		t.Error("Alice è al tetto")
	}
}

func TestCoda_CapacitaTotale(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	for _, n := range []string{"a", "b", "c"} {
		if _, err := e.eng.OnPush(ctx, pushOf(e.repo(uuid.New(), n), uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x")); err != nil {
			t.Fatal(err)
		}
	}
	// 2 posti, 3 owner con un job ciascuno: ne partono 2.
	for i := 0; i < 2; i++ {
		if _, _, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 2, "pod"); err != nil || !ok {
			t.Fatalf("claim %d: %v %v", i, ok, err)
		}
	}
	if _, _, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 2, "pod"); err != nil || ok {
		t.Fatalf("oltre la capacità: %v %v", ok, err)
	}
}

func TestCoda_UnSoloOwnerNonSuperaLaMeta(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "solo")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/ci.yml", "  push:")}
	for _, b := range []string{"b1", "b2", "b3"} {
		if _, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/"+b, sha("9"), sha("a"), "x")); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for {
		_, _, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 3, "pod")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		n++
	}
	if n != ci.OwnerLimit(3) {
		t.Errorf("partiti %d job, tetto per owner %d", n, ci.OwnerLimit(3))
	}
}

func TestCoda_JobConNeeds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	e.src.files[sha("a")] = []ci.File{{Path: ".github/workflows/ci.yml", Content: []byte(`on: push
jobs:
  build:
    runs-on: ubuntu-latest
  test:
    needs: build
    runs-on: ubuntu-latest
  deploy:
    needs: [build, test]
    runs-on: ubuntu-latest
`)}}
	ids, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x"))
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	status := func() map[string]string {
		run, _ := ci.GetRun(ctx, e.pool, repo, ids[0])
		m := map[string]string{}
		for _, j := range run.Jobs {
			m[j.Key] = j.Status
		}
		return m
	}
	if s := status(); s["build"] != "queued" || s["test"] != "waiting" || s["deploy"] != "waiting" {
		t.Fatalf("stato iniziale %v", s)
	}
	step := func(concl string) {
		t.Helper()
		job, _, ok, err := ci.ClaimNext(ctx, e.pool, *e.clock, 3, "pod")
		if err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		if err := ci.CompleteJob(ctx, e.pool, *e.clock, job.ID, concl); err != nil {
			t.Fatal(err)
		}
	}
	step("success") // build
	if s := status(); s["test"] != "queued" || s["deploy"] != "waiting" {
		t.Fatalf("dopo build %v", s)
	}
	step("failure") // test
	s := status()
	if s["deploy"] != "completed" {
		t.Fatalf("deploy doveva essere saltato: %v", s)
	}
	run, _ := ci.GetRun(ctx, e.pool, repo, ids[0])
	if run.Status != "completed" || run.Conclusion == nil || *run.Conclusion != "failure" {
		t.Errorf("run: %s %v", run.Status, run.Conclusion)
	}
	for _, j := range run.Jobs {
		if j.Key == "deploy" && (j.Conclusion == nil || *j.Conclusion != "skipped") {
			t.Errorf("deploy: %v", j.Conclusion)
		}
	}
}

const cronWF = "  schedule:\n    - cron: '0 * * * *'\n"

func TestSchedule_ScattaUnaVoltaPerScadenza(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/nightly.yml", cronWF)}
	// Il push sul branch principale registra l'orario.
	p := ci.Push{RepoID: repo, Pusher: uuid.New(), Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("9"), After: sha("a"), IsDefaultBranch: true, Message: "x"}}}
	if _, err := e.eng.OnPush(ctx, p); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_schedules WHERE repo_id = $1`, repo).Scan(&n)
	if n != 1 {
		t.Fatalf("schedule registrati %d", n)
	}
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("prima della scadenza: %v %v", ids, err)
	}
	e.advance(61 * time.Minute)
	ids, err := e.eng.Tick(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("alla scadenza: %v %v", ids, err)
	}
	run, _ := ci.GetRun(ctx, e.pool, repo, ids[0])
	if run.Event != "schedule" || run.ActorID != nil || run.Ref != "refs/heads/main" || run.SHA != sha("a") || run.ScheduleSlot == nil {
		t.Errorf("run %+v", run)
	}
	// Un secondo giro subito dopo non lo ripete.
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 0 {
		t.Errorf("doppione: %v %v", ids, err)
	}
	// Due repliche sulla stessa scadenza: l'indice sullo slot impedisce il doppione.
	e.exec(`UPDATE core.ci_schedules SET next_run_at = $2 WHERE repo_id = $1`, repo, *run.ScheduleSlot)
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 0 {
		t.Errorf("stessa scadenza ripetuta: %v %v", ids, err)
	}
	if got := e.count(repo, "queued"); got != 1 {
		t.Errorf("esecuzioni in coda %d", got)
	}
	// Un'ora dopo scatta di nuovo.
	e.advance(time.Hour)
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 1 {
		t.Errorf("scatto successivo: %v %v", ids, err)
	}
}

func TestSchedule_SospesoDopo60GiorniSenzaPush(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/nightly.yml", cronWF)}
	p := ci.Push{RepoID: repo, Pusher: uuid.New(), Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("9"), After: sha("a"), IsDefaultBranch: true, Message: "x"}}}
	if _, err := e.eng.OnPush(ctx, p); err != nil {
		t.Fatal(err)
	}
	// 59 giorni dopo: scatta ancora.
	e.advance(59 * 24 * time.Hour)
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 1 {
		t.Fatalf("a 59 giorni: %v %v", ids, err)
	}
	// Oltre i 60 giorni dall'ultimo push: sospeso, nessuna esecuzione.
	e.advance(2*24*time.Hour + time.Hour)
	ids, err := e.eng.Tick(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("a 61 giorni: %v %v", ids, err)
	}
	var suspended *time.Time
	if err := e.pool.QueryRow(ctx, `SELECT suspended_at FROM core.ci_schedules WHERE repo_id = $1`, repo).Scan(&suspended); err != nil || suspended == nil {
		t.Fatalf("sospensione non registrata: %v %v", suspended, err)
	}
	// Resta sospeso nei giri successivi.
	e.advance(5 * time.Hour)
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("ancora sospeso: %v %v", ids, err)
	}
	before := len(e.runs(repo))
	// Un push (anche su un altro branch) lo riattiva: riparte dal prossimo scatto, senza recuperare i persi.
	e.src.files[sha("b")] = e.src.files[sha("a")]
	if _, err := e.eng.OnPush(ctx, pushOf(repo, uuid.New(), "refs/heads/dev", sha("9"), sha("b"), "x")); err != nil {
		t.Fatal(err)
	}
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("subito dopo il push: %v %v", ids, err)
	}
	e.advance(61 * time.Minute)
	if ids, err := e.eng.Tick(ctx); err != nil || len(ids) != 1 {
		t.Fatalf("dopo la riattivazione: %v %v", ids, err)
	}
	if got := len(e.runs(repo)); got != before+2 { // la run del push su dev non c'è (no push trigger); +1 schedule
		// il workflow ha solo schedule: il push su dev non crea run
		if got != before+1 {
			t.Errorf("esecuzioni %d, prima %d", got, before)
		}
	}
}

func TestSchedule_RepoArchiviatoOMirrorNonScatta(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/nightly.yml", cronWF)}
	mk := func(name string) uuid.UUID {
		repo := e.repo(uuid.New(), name)
		p := ci.Push{RepoID: repo, Pusher: uuid.New(), Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("9"), After: sha("a"), IsDefaultBranch: true, Message: "x"}}}
		if _, err := e.eng.OnPush(ctx, p); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	archived, mirror, normal := mk("arch"), mk("mirror"), mk("normal")
	e.exec(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, archived)
	e.exec(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, created_by) VALUES ($1, $2, 'https://example.org/o/r.git', $3)`,
		uuid.New(), mirror, uuid.New())
	e.advance(61 * time.Minute)
	if _, err := e.eng.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.runs(archived)); n != 0 {
		t.Errorf("archiviato: %d", n)
	}
	if n := len(e.runs(mirror)); n != 0 {
		t.Errorf("mirror in pull: %d", n)
	}
	if n := len(e.runs(normal)); n != 1 {
		t.Errorf("normale: %d", n)
	}
}

func TestSchedule_UnaModificaDelWorkflowAggiornaGliOrari(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	main := func(n string) ci.Push {
		return ci.Push{RepoID: repo, Pusher: uuid.New(), Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("9"), After: sha(n), IsDefaultBranch: true, Message: "x"}}}
	}
	e.src.files[sha("a")] = []ci.File{wfFile(".github/workflows/nightly.yml", cronWF)}
	e.src.files[sha("b")] = []ci.File{wfFile(".github/workflows/nightly.yml", "  workflow_dispatch:")} // niente schedule
	e.src.files[sha("c")] = []ci.File{wfFile(".github/workflows/nightly.yml", "  schedule:\n    - cron: '* * * * *'\n")}
	count := func() int {
		var n int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_schedules WHERE repo_id = $1`, repo).Scan(&n)
		return n
	}
	for sh, want := range map[string]int{"a": 1, "b": 0, "c": 0} { // c: ogni minuto, rifiutato
		if _, err := e.eng.OnPush(ctx, main(sh)); err != nil {
			t.Fatal(err)
		}
		if got := count(); got != want {
			t.Errorf("dopo %s: %d schedule, attesi %d", sh, got, want)
		}
	}
}

func TestDispatch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	actor := uuid.New()
	e.src.files[sha("a")] = []ci.File{
		{Path: ".github/workflows/deploy.yml", Content: []byte("on:\n  workflow_dispatch:\n    inputs:\n      env:\n        required: true\n      level:\n        default: info\njobs:\n  d:\n    runs-on: ubuntu-latest\n")},
		wfFile(".github/workflows/ci.yml", "  push:"),
	}
	d := ci.Dispatch{RepoID: repo, Workflow: "deploy.yml", Ref: "refs/heads/main", SHA: sha("a"), Actor: actor, Inputs: map[string]string{"env": "prod"}}
	id, err := e.eng.Dispatch(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := ci.GetRun(ctx, e.pool, repo, id)
	if run.Event != "workflow_dispatch" || run.Inputs["env"] != "prod" || run.Inputs["level"] != "info" || run.ActorID == nil || *run.ActorID != actor {
		t.Errorf("run %+v", run)
	}
	// Per percorso intero, e due avvii non si annullano fra loro.
	d.Workflow = ".github/workflows/deploy.yml"
	id2, err := e.eng.Dispatch(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := ci.GetRun(ctx, e.pool, repo, id); r.Status != "queued" || id == id2 {
		t.Errorf("un secondo avvio non annulla il primo: %s", r.Status)
	}
	d.Workflow = "ci.yml"
	if _, err := e.eng.Dispatch(ctx, d); !errors.Is(err, ci.ErrNotDispatchable) {
		t.Errorf("senza workflow_dispatch: %v", err)
	}
	d.Workflow = "nonc'e.yml"
	if _, err := e.eng.Dispatch(ctx, d); !errors.Is(err, ci.ErrWorkflowNotFound) {
		t.Errorf("inesistente: %v", err)
	}
	d.Workflow, d.Inputs = "deploy.yml", nil
	if _, err := e.eng.Dispatch(ctx, d); !errors.Is(err, ci.ErrInvalidInputs) {
		t.Errorf("input obbligatorio mancante: %v", err)
	}
	// [skip ci] non vale per un avvio esplicito.
	e.src.messages[sha("a")] = "x [skip ci]"
	d.Inputs = map[string]string{"env": "x"}
	if _, err := e.eng.Dispatch(ctx, d); err != nil {
		t.Errorf("l'avvio manuale ignora [skip ci]: %v", err)
	}
}

func TestDispatchPods_RiportaTimedOut(t *testing.T) {
	e := newEnv(t)
	jobID := queuedInternalJob(t, e)
	pods := &fakePods{conclusion: ci.ConclusionTimedOut}
	reporter := &captureReporter{}
	started, err := ci.DispatchPods(context.Background(), e.pool, pods,
		ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1}, *e.clock, reporter)
	if err != nil || started != 1 {
		t.Fatalf("dispatch: started=%d err=%v", started, err)
	}
	waitJobConclusion(t, e, jobID, ci.ConclusionTimedOut)
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if len(reporter.changes) < 2 {
		t.Fatalf("report attesi per presa e chiusura, trovati %d", len(reporter.changes))
	}
}

func TestReconcile_RiprendePodVivoEOmetteAssente(t *testing.T) {
	t.Run("pod assente chiude failure", func(t *testing.T) {
		e := newEnv(t)
		jobID := queuedInternalJob(t, e)
		if _, _, ok, err := ci.ClaimNext(context.Background(), e.pool, *e.clock, 1, "internal-pod"); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		reporter := &captureReporter{}
		if err := ci.Reconcile(context.Background(), e.pool, &fakePods{exists: false}, "gitstack-ci-jobs", nil, *e.clock, reporter); err != nil {
			t.Fatal(err)
		}
		waitJobConclusion(t, e, jobID, ci.ConclusionFailure)
	})

	t.Run("pod vivo riattacca watcher", func(t *testing.T) {
		e := newEnv(t)
		jobID := queuedInternalJob(t, e)
		if _, _, ok, err := ci.ClaimNext(context.Background(), e.pool, *e.clock, 1, "internal-pod"); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		pods := &fakePods{exists: true, conclusion: ci.ConclusionSuccess}
		if err := ci.Reconcile(context.Background(), e.pool, pods, "gitstack-ci-jobs", nil, *e.clock, &captureReporter{}); err != nil {
			t.Fatal(err)
		}
		waitJobConclusion(t, e, jobID, ci.ConclusionSuccess)
	})
}

// GIT-371: runs-on con la label gitstack-build resta un job interno e il dispatcher crea il pod
// nel namespace di build (con il sidecar di BuildKit); senza la build configurata il job fallisce.
func TestDispatchPods_GitstackBuild(t *testing.T) {
	run := func(t *testing.T, cfg ci.RunnerConfig) (*fakePods, uuid.UUID, *env) {
		e := newEnv(t)
		repo := e.repo(uuid.New(), "builder")
		e.src.files[sha("a")] = []ci.File{{Path: ".github/workflows/img.yml", Content: []byte("on: push\njobs:\n  img:\n    runs-on: [ubuntu-latest, gitstack-build]\n")}}
		ids, err := e.eng.OnPush(context.Background(), pushOf(repo, uuid.New(), "refs/heads/main", sha("9"), sha("a"), "x"))
		if err != nil || len(ids) != 1 {
			t.Fatalf("accodamento: %v %v", ids, err)
		}
		var jobID uuid.UUID
		var typ string
		var runsOn []string
		if err := e.pool.QueryRow(context.Background(), `SELECT id, runner_type, runs_on FROM core.ci_jobs WHERE run_id = $1`, ids[0]).Scan(&jobID, &typ, &runsOn); err != nil {
			t.Fatal(err)
		}
		if typ != "internal_pod" || !ci.IsBuildJob(runsOn) {
			t.Fatalf("runner_type %q runs_on %v", typ, runsOn)
		}
		pods := &fakePods{conclusion: ci.ConclusionSuccess}
		_, err = ci.DispatchPods(context.Background(), e.pool, pods, cfg, *e.clock, &captureReporter{})
		if err != nil {
			t.Fatal(err)
		}
		return pods, jobID, e
	}
	t.Run("pod di build", func(t *testing.T) {
		pods, jobID, e := run(t, ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1,
			BuildNamespace: "gitstack-ci-build", BuildImage: "moby/buildkit:rootless", RegistryAddr: "gw.ns.svc:8080"})
		waitJobConclusion(t, e, jobID, ci.ConclusionSuccess)
		if len(pods.created) != 1 || pods.created[0].Namespace != "gitstack-ci-build" || pods.created[0].BuildKit == nil ||
			pods.created[0].Env["GITSTACK_REGISTRY"] != "gw.ns.svc:8080" {
			t.Fatalf("pod creati: %+v", pods.created)
		}
	})
	t.Run("build non configurata", func(t *testing.T) {
		pods, jobID, e := run(t, ci.RunnerConfig{Image: "runner:test", GatewayURL: "http://core:8081", Capacity: 1})
		waitJobConclusion(t, e, jobID, ci.ConclusionFailure)
		if len(pods.created) != 0 {
			t.Fatalf("nessun pod senza build configurata: %+v", pods.created)
		}
	})
}
