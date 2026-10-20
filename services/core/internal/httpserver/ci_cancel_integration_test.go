//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// jobsYML ha tre job: build e lint subito in coda, test in attesa di build.
const jobsYML = "name: Deploy\non:\n  workflow_dispatch:\n    inputs:\n      env:\n        required: true\njobs:\n  build:\n    runs-on: ubuntu-latest\n  lint:\n    runs-on: ubuntu-latest\n  test:\n    runs-on: ubuntu-latest\n    needs: build\n"

// ciJobsGit usa jobsYML per il workflow di dispatch. gone=true toglie il
// workflow dall'albero: un rerun su quello sha non lo trova più (GIT-314).
type ciJobsGit struct {
	*ciGit
	gone bool
}

func (g *ciJobsGit) ReadJSON(ctx context.Context, c trust.Identity, repo uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	switch {
	case path == "tree" && q.Get("path") == ".github/workflows" && g.gone:
		return json.RawMessage(`{"ref":"x","commitSha":"x","path":".github/workflows","entries":[],"truncated":false}`), nil
	case path == "contents" && q.Get("path") == ".github/workflows/deploy.yml":
		b, _ := json.Marshal(jobsYML)
		return json.RawMessage(`{"ref":"x","path":".github/workflows/deploy.yml","name":"x","sha":"x","size":1,"binary":false,"kind":"text","display":"highlight","truncated":false,"content":` + string(b) + `,"lastCommit":` + commitJSON + `}`), nil
	}
	return g.ciGit.ReadJSON(ctx, c, repo, path, q)
}

// newCICancelEnv: alice ha app (interno: lo legge chiunque) e secret (privato:
// carol non lo legge).
func newCICancelEnv(t *testing.T) (*reposEnv, *ciJobsGit) {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	git := &ciJobsGit{ciGit: &ciGit{&readerGit{fakeGit: newFakeGit()}}}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
	e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
	e.create("alice", `{"owner":"alice","name":"other","visibility":"internal"}`)
	e.create("alice", `{"owner":"alice","name":"secret","visibility":"private"}`)
	return e, git
}

type ciRunJSON struct {
	Id, Status, Event, Ref, Sha, WorkflowId string
	Conclusion                              *string
	Jobs                                    []ciJobJSON
}

type ciJobJSON struct {
	Id, Name, Status string
	Conclusion       *string
}

func decodeCIRun(t *testing.T, body []byte) ciRunJSON {
	t.Helper()
	var r ciRunJSON
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("esecuzione non valida: %v", err)
	}
	return r
}

func decodeCIJob(t *testing.T, body []byte) ciJobJSON {
	t.Helper()
	var j ciJobJSON
	if err := json.Unmarshal(body, &j); err != nil {
		t.Fatalf("job non valido: %v", err)
	}
	return j
}

// byName indicizza i job di un'esecuzione per nome.
func ciByName(r ciRunJSON) map[string]ciJobJSON {
	out := map[string]ciJobJSON{}
	for _, j := range r.Jobs {
		out[j.Name] = j
	}
	return out
}

// dispatch avvia deploy.yml su base (un repo di alice) con env=prod.
func ciDispatch(e *reposEnv, base string) ciRunJSON {
	e.t.Helper()
	rec := e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"main","inputs":{"env":"prod"}}`)
	e.want(rec, http.StatusAccepted)
	return decodeCIRun(e.t, rec.Body.Bytes())
}

// drain fa girare tutti i job in coda con successo, uno alla volta (un owner
// ha un solo posto con capacity 3): a fine l'esecuzione è conclusa.
func ciDrain(t *testing.T, e *reposEnv) {
	t.Helper()
	ctx := context.Background()
	for {
		job, _, ok, err := ci.ClaimNext(ctx, e.pool, time.Now().UTC(), 3, "runner-test")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
		if err := ci.CompleteJob(ctx, e.pool, time.Now().UTC(), job.ID, ci.ConclusionSuccess); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCI_CancelRunInCoda: un run in coda va in cancelled con tutti i job annullati.
func TestCI_CancelRunInCoda(t *testing.T) {
	e, _ := newCICancelEnv(t)
	base := "/repos/alice/app"
	run := ciDispatch(e, base)
	if run.Status != "queued" {
		t.Fatalf("stato iniziale %q", run.Status)
	}

	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "bob", ""), http.StatusForbidden)

	rec := e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "alice", "")
	e.want(rec, http.StatusAccepted)
	got := decodeCIRun(t, rec.Body.Bytes())
	if got.Status != "cancelled" || got.Conclusion == nil || *got.Conclusion != "cancelled" {
		t.Fatalf("run dopo il cancel: %s", rec.Body.String())
	}
	for name, j := range ciByName(got) {
		if j.Status != "cancelled" || j.Conclusion == nil || *j.Conclusion != "cancelled" {
			t.Errorf("job %s: %s/%v, voluto cancelled/cancelled", name, j.Status, j.Conclusion)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.ci_jobs WHERE run_id = $1 AND cancel_requested_at IS NOT NULL`, uuid.MustParse(run.Id)); n != 0 {
		t.Errorf("richiesta di annullamento su job in coda: %d", n)
	}

	// Già concluso: 409.
	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "alice", ""), http.StatusConflict)
	// Lo stato letto dal repo è quello annullato.
	rec = e.do("GET", base+"/actions/runs/"+run.Id, "bob", "")
	e.want(rec, http.StatusOK)
	if decodeCIRun(t, rec.Body.Bytes()).Status != "cancelled" {
		t.Errorf("lettura dopo il cancel: %s", rec.Body.String())
	}
}

// TestCI_CancelRunInCorso: il job in corso resta in_progress con la richiesta
// di annullamento; gli altri vanno cancelled; il runner lo chiude dopo, e il run
// resta annullato anche se il job finisce con successo.
func TestCI_CancelRunInCorso(t *testing.T) {
	e, _ := newCICancelEnv(t)
	base := "/repos/alice/app"
	run := ciDispatch(e, base)

	ctx := context.Background()
	job, _, ok, err := ci.ClaimNext(ctx, e.pool, time.Now().UTC(), 3, "runner-test")
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if job.Key != "build" {
		t.Fatalf("il primo job in coda doveva essere build, è %q", job.Key)
	}

	rec := e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "alice", "")
	e.want(rec, http.StatusAccepted)
	got := decodeCIRun(t, rec.Body.Bytes())
	if got.Status != "cancelled" {
		t.Fatalf("run in corso dopo il cancel: %s", rec.Body.String())
	}
	jobs := ciByName(got)
	if jobs["build"].Status != "in_progress" {
		t.Errorf("build: %s, voluto in_progress (la richiesta non cambia il job)", jobs["build"].Status)
	}
	for _, name := range []string{"lint", "test"} {
		if jobs[name].Status != "cancelled" || jobs[name].Conclusion == nil || *jobs[name].Conclusion != "cancelled" {
			t.Errorf("%s: %s/%v, voluto cancelled", name, jobs[name].Status, jobs[name].Conclusion)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.ci_jobs WHERE id = $1 AND cancel_requested_at IS NOT NULL`, job.ID); n != 1 {
		t.Fatalf("richiesta di annullamento sul job in corso non registrata")
	}

	// Il runner chiude il job con successo (è finito prima di vedere la
	// richiesta): il run resta annullato.
	if err := ci.CompleteJob(ctx, e.pool, time.Now().UTC(), job.ID, ci.ConclusionSuccess); err != nil {
		t.Fatal(err)
	}
	rec = e.do("GET", base+"/actions/runs/"+run.Id, "bob", "")
	e.want(rec, http.StatusOK)
	final := decodeCIRun(t, rec.Body.Bytes())
	if final.Status != "cancelled" || final.Conclusion == nil || *final.Conclusion != "cancelled" {
		t.Errorf("run dopo la chiusura del job in corso: %s/%v, voluto cancelled", final.Status, final.Conclusion)
	}
	if ciByName(final)["build"].Status != "completed" {
		t.Errorf("build dopo CompleteJob: %s", ciByName(final)["build"].Status)
	}
	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "alice", ""), http.StatusConflict)
}

// TestCI_CancelIsolamento: un altro repo o un repo non leggibile non vede l'esecuzione (404).
func TestCI_CancelIsolamento(t *testing.T) {
	e, _ := newCICancelEnv(t)
	run := ciDispatch(e, "/repos/alice/app")
	secret := ciDispatch(e, "/repos/alice/secret")

	e.want(e.do("POST", "/repos/alice/other/actions/runs/"+run.Id+"/cancel", "alice", ""), http.StatusNotFound)
	e.want(e.do("POST", "/repos/alice/app/actions/runs/"+uuid.NewString()+"/cancel", "alice", ""), http.StatusNotFound)
	e.want(e.do("POST", "/repos/alice/secret/actions/runs/"+secret.Id+"/cancel", "carol", ""), http.StatusNotFound)
	// Nulla è stato annullato.
	if n := e.count(`SELECT count(*) FROM core.ci_runs WHERE status = 'cancelled'`); n != 0 {
		t.Fatalf("esecuzioni annullate per errore: %d", n)
	}
}

// TestCI_RerunEsecuzioneConclusa: un run concluso si riesegue in un run nuovo
// con lo stesso workflow, ref, sha, evento e input.
func TestCI_RerunEsecuzioneConclusa(t *testing.T) {
	e, git := newCICancelEnv(t)
	base := "/repos/alice/app"
	old := ciDispatch(e, base)

	// Non ancora concluso: 409; senza write: 403.
	e.want(e.do("POST", base+"/actions/runs/"+old.Id+"/rerun", "alice", ""), http.StatusConflict)
	e.want(e.do("POST", base+"/actions/runs/"+old.Id+"/rerun", "bob", ""), http.StatusForbidden)

	ciDrain(t, e)
	rec := e.do("GET", base+"/actions/runs/"+old.Id, "bob", "")
	e.want(rec, http.StatusOK)
	if s := decodeCIRun(t, rec.Body.Bytes()).Status; s != "completed" {
		t.Fatalf("run dopo i job: %s, voluto completed", s)
	}

	// Un altro repo non lo vede.
	e.want(e.do("POST", "/repos/alice/other/actions/runs/"+old.Id+"/rerun", "alice", ""), http.StatusNotFound)

	rec = e.do("POST", base+"/actions/runs/"+old.Id+"/rerun", "alice", "")
	e.want(rec, http.StatusAccepted)
	nu := decodeCIRun(t, rec.Body.Bytes())
	if nu.Id == "" || nu.Id == old.Id {
		t.Fatalf("il rerun deve creare un run nuovo: %s", rec.Body.String())
	}
	if nu.Sha != old.Sha || nu.Ref != old.Ref || nu.Event != old.Event || nu.WorkflowId != old.WorkflowId {
		t.Errorf("run nuovo: sha/ref/evento/workflow non coincidono: %s", rec.Body.String())
	}
	if nu.Status != "queued" || len(nu.Jobs) != 3 {
		t.Errorf("run nuovo: stato %q con %d job", nu.Status, len(nu.Jobs))
	}
	for name, j := range ciByName(nu) {
		if j.Status != "queued" {
			t.Errorf("job %s del run nuovo: %s, voluto queued", name, j.Status)
		}
	}
	if n := e.count(`SELECT count(*) FROM core.ci_runs WHERE id = $1 AND inputs->>'env' = 'prod'`, uuid.MustParse(nu.Id)); n != 1 {
		t.Errorf("gli input del run originale non sono stati copiati")
	}

	// Il workflow non c'è più a quello sha: 409.
	ciDrain(t, e)
	git.gone = true
	e.want(e.do("POST", base+"/actions/runs/"+nu.Id+"/rerun", "alice", ""), http.StatusConflict)
	git.gone = false
}

// TestCI_RerunDopoAnnullamento: un run annullato è concluso e si riesegue;
// e un repo archiviato non accetta il rerun.
func TestCI_RerunDopoAnnullamento(t *testing.T) {
	e, _ := newCICancelEnv(t)
	base := "/repos/alice/app"
	run := ciDispatch(e, base)
	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/cancel", "alice", ""), http.StatusAccepted)
	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/rerun", "alice", ""), http.StatusAccepted)

	if _, err := e.pool.Exec(context.Background(), `UPDATE core.repositories SET archived_at = now() WHERE name = 'app'`); err != nil {
		t.Fatal(err)
	}
	e.want(e.do("POST", base+"/actions/runs/"+run.Id+"/rerun", "alice", ""), http.StatusConflict)
}

// TestCI_JobSingolo: GET di un job; un job di un altro repo o di un repo non
// leggibile è 404.
func TestCI_JobSingolo(t *testing.T) {
	e, _ := newCICancelEnv(t)
	base := "/repos/alice/app"
	run := ciDispatch(e, base)
	build := ciByName(run)["build"]

	rec := e.do("GET", base+"/actions/jobs/"+build.Id, "bob", "")
	e.want(rec, http.StatusOK)
	j := decodeCIJob(t, rec.Body.Bytes())
	if j.Id != build.Id || j.Name != "build" || j.Status != "queued" {
		t.Errorf("job: %s", rec.Body.String())
	}
	// test aspetta build: nell'API è queued.
	if ciByName(run)["test"].Status != "queued" {
		t.Errorf("test: %s", ciByName(run)["test"].Status)
	}

	// Il job di app letto come job di other: 404.
	e.want(e.do("GET", "/repos/alice/other/actions/jobs/"+build.Id, "alice", ""), http.StatusNotFound)
	// Job inesistente: 404.
	e.want(e.do("GET", base+"/actions/jobs/"+uuid.NewString(), "alice", ""), http.StatusNotFound)

	// Repo privato non leggibile da carol: 404 anche con un job reale.
	secret := ciDispatch(e, "/repos/alice/secret")
	sj := ciByName(secret)["build"]
	e.want(e.do("GET", "/repos/alice/secret/actions/jobs/"+sj.Id, "carol", ""), http.StatusNotFound)
	e.want(e.do("GET", "/repos/alice/secret/actions/jobs/"+sj.Id, "alice", ""), http.StatusOK)
}
