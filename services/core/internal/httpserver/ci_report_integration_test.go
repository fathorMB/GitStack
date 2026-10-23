//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/google/uuid"
)

// GIT-281 (v2-a, CI6): stato del commit per ogni job («workflow / job»), gate del
// merge con un job rosso, notifiche a chi ha avviato l'esecuzione su fallimento
// e ritorno al verde, webhook workflow_run e workflow_job firmati.
//
// Il motore della CI gira sullo stesso database con il Reporter di core
// (httpserver.NewCIReporter); i job li prende e li chiude il test, come farebbe
// il ci-gateway con ClaimNextReported e CompleteJobReported. Il workflow è
// jobsYML (build e lint in coda, test dopo build) di ci_cancel_integration_test.

// g281Source serve sempre deploy.yml: il motore non legge git in questi test.
type g281Source struct{ ci.Source }

func (g281Source) Workflows(context.Context, uuid.UUID, string) ([]ci.File, error) {
	return []ci.File{{Path: ".github/workflows/deploy.yml", Content: []byte(jobsYML)}}, nil
}

type g281Env struct {
	*pevEnv
	t      *testing.T
	repoID uuid.UUID
	ciEng  *ci.Engine
	rep    ci.Reporter
}

func newG281Env(t *testing.T) *g281Env {
	t.Helper()
	pe := newPevEnv(t)
	rep := httpserver.NewCIReporter(pe.pool, pe.opts...)
	e := &g281Env{pevEnv: pe, t: t, rep: rep}
	e.ciEng = &ci.Engine{Pool: pe.pool, Source: g281Source{}, Now: time.Now, Reporter: rep}
	e.repoID = e.repo("app", true)
	return e
}

// dispatch avvia deploy.yml a mano, per alice, su ref e sha.
func (e *g281Env) dispatch(ref, sha string) uuid.UUID {
	e.t.Helper()
	id, err := e.ciEng.Dispatch(context.Background(), ci.Dispatch{RepoID: e.repoID, Workflow: "deploy.yml", Ref: ref,
		SHA: sha, Actor: uuid.MustParse(users["alice"]), Inputs: map[string]string{"env": "prod"}})
	if err != nil {
		e.t.Fatalf("dispatch: %v", err)
	}
	return id
}

// finish prende i job in coda uno alla volta (un owner ha un posto) e chiude
// ciascuno con l'esito di conclFor(nome del job), finché la coda è vuota.
func (e *g281Env) finish(conclFor func(job string) string) {
	e.t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		job, _, _, ok, err := ci.ClaimNextReported(ctx, e.pool, time.Now().UTC(), 3, "pod-1", 0, e.rep)
		if err != nil {
			e.t.Fatalf("claim: %v", err)
		}
		if !ok {
			return
		}
		if err := ci.CompleteJobReported(ctx, e.pool, time.Now().UTC(), job.ID, conclFor(job.Name), e.rep); err != nil {
			e.t.Fatalf("chiusura di %s: %v", job.Name, err)
		}
	}
	e.t.Fatal("la coda non si svuota")
}

// statuses ritorna contesto → stato dello sha, dal database.
func (e *g281Env) statuses(sha string) map[string]string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT context, state FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2`, e.repoID, sha)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, s string
		if err := rows.Scan(&c, &s); err != nil {
			e.t.Fatal(err)
		}
		out[c] = s
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *g281Env) wantStatuses(label, sha string, want map[string]string) {
	e.t.Helper()
	if got := e.statuses(sha); !maps.Equal(got, want) {
		e.t.Fatalf("%s: stati = %v, voluti %v", label, got, want)
	}
}

// Stato per job: pending alla creazione, poi il risultato di ciascun job; un job
// saltato (la sua dipendenza è fallita) è error con la ragione nella descrizione.
// A fine esecuzione: notifica a chi l'ha avviata, webhook firmati.
func TestCI_StatoPerJobNotificheEWebhook_GIT281(t *testing.T) {
	e := newG281Env(t)
	rec := e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["workflow_run","workflow_job"],"secret":"s3cr3t-ci"}`, hookURL))
	e.hookIDOf(rec)

	const sha1 = "1111111111111111111111111111111111111111"
	e.dispatch("refs/heads/main", sha1)
	pending := map[string]string{"Deploy / build": "pending", "Deploy / lint": "pending", "Deploy / test": "pending"}
	e.wantStatuses("alla creazione", sha1, pending)

	job, _, _, ok, err := ci.ClaimNextReported(context.Background(), e.pool, time.Now().UTC(), 3, "pod-1", 0, e.rep)
	if err != nil || !ok || job.Name != "build" {
		t.Fatalf("primo job preso: %+v ok=%v err=%v", job, ok, err)
	}
	if err := ci.CompleteJobReported(context.Background(), e.pool, time.Now().UTC(), job.ID, ci.ConclusionFailure, e.rep); err != nil {
		t.Fatal(err)
	}
	e.wantStatuses("build rosso", sha1, map[string]string{"Deploy / build": "failure", "Deploy / lint": "pending", "Deploy / test": "error"})
	var desc string
	if err := e.pool.QueryRow(context.Background(), `SELECT description FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2 AND context = 'Deploy / test'`,
		e.repoID, sha1).Scan(&desc); err != nil || !strings.HasPrefix(desc, "Saltato") {
		t.Fatalf("descrizione del job saltato: %q (%v)", desc, err)
	}

	e.finish(func(string) string { return ci.ConclusionSuccess })
	e.wantStatuses("esecuzione conclusa", sha1, map[string]string{"Deploy / build": "failure", "Deploy / lint": "success", "Deploy / test": "error"})
	var runStatus, runConcl string
	if err := e.pool.QueryRow(context.Background(), `SELECT status, conclusion FROM core.ci_runs WHERE repo_id = $1 AND sha = $2`,
		e.repoID, sha1).Scan(&runStatus, &runConcl); err != nil || runStatus != "completed" || runConcl != "failure" {
		t.Fatalf("esecuzione: %s/%s (%v)", runStatus, runConcl, err)
	}

	// Notifica: solo a chi ha avviato (alice), per il fallimento.
	e.expect("fallimento", "alice:state_change")

	// Webhook: ogni consegna è firmata e ha l'intestazione del tipo.
	e.run()
	var sawRunCompleted, sawJobCompleted bool
	for _, r := range e.recv.requests() {
		verifySignature(t, "s3cr3t-ci", r)
		ev := r.header.Get("X-GitStack-Event")
		if ev != "workflow_run" && ev != "workflow_job" {
			t.Fatalf("intestazione X-GitStack-Event = %q", ev)
		}
		var p struct {
			Action      string `json:"action"`
			WorkflowRun struct {
				Conclusion *string `json:"conclusion"`
			} `json:"workflowRun"`
		}
		if err := json.Unmarshal(r.body, &p); err != nil {
			t.Fatal(err)
		}
		if ev == "workflow_run" && p.Action == "completed" && p.WorkflowRun.Conclusion != nil && *p.WorkflowRun.Conclusion == "failure" {
			sawRunCompleted = true
		}
		if ev == "workflow_job" && p.Action == "completed" {
			sawJobCompleted = true
		}
	}
	if !sawRunCompleted || !sawJobCompleted {
		t.Fatalf("consegne mancanti: run completed=%v, job completed=%v", sawRunCompleted, sawJobCompleted)
	}

	// Ritorno al verde: la nuova esecuzione dello stesso workflow sullo stesso ref
	// riesce dopo una fallita. Avvisa alice con recovered=true.
	const sha2 = "2222222222222222222222222222222222222222"
	e.dispatch("refs/heads/main", sha2)
	e.finish(func(string) string { return ci.ConclusionSuccess })
	e.wantStatuses("secondo commit verde", sha2, map[string]string{"Deploy / build": "success", "Deploy / lint": "success", "Deploy / test": "success"})
	e.process()
	if n := e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND reason = 'state_change' AND data->>'recovered' = 'true'`,
		uuid.MustParse(users["alice"])); n != 1 {
		t.Fatalf("notifiche di ritorno al verde = %d, voleva 1", n)
	}
	e.sql(`DELETE FROM core.notifications`)

	// Verde dopo verde: nessun avviso.
	const sha3 = "3333333333333333333333333333333333333333"
	e.dispatch("refs/heads/main", sha3)
	e.finish(func(string) string { return ci.ConclusionSuccess })
	e.expect("verde dopo verde")
}

// Gate del merge: con «Deploy / build» obbligatorio, il job rosso blocca la PR
// (status_failed), il job nuovo in corso blocca come mancante (status_required),
// il verde sblocca.
func TestMergeGate_JobRossoBloccaIlMerge_GIT281(t *testing.T) {
	e := newG281Env(t)
	e.id.grantWrite(e.repoID, "bob")
	e.branch(e.repoID, "feat")
	n := e.open("app", "bob", "feat")
	head := e.git.of(e.repoID)["feat"]
	e.want(e.settings("app", "alice", `{"requiredStatusContexts":["Deploy / build"]}`), http.StatusOK, "")

	if got := e.blockers("app", n); !slices.Equal(got, []string{"status_required"}) {
		t.Fatalf("prima di qualsiasi esecuzione: %v", got)
	}
	e.dispatch("refs/heads/feat", head)
	if got := e.blockers("app", n); !slices.Equal(got, []string{"status_required"}) {
		t.Fatalf("con il job in coda: %v", got)
	}
	job, _, _, ok, err := ci.ClaimNextReported(context.Background(), e.pool, time.Now().UTC(), 3, "pod-1", 0, e.rep)
	if err != nil || !ok || job.Name != "build" {
		t.Fatalf("job preso: %+v ok=%v err=%v", job, ok, err)
	}
	if err := ci.CompleteJobReported(context.Background(), e.pool, time.Now().UTC(), job.ID, ci.ConclusionFailure, e.rep); err != nil {
		t.Fatal(err)
	}
	m := e.mergeability("app", n)
	if m.Mergeable || len(m.Blockers) != 1 || m.Blockers[0].Code != "status_failed" || m.Blockers[0].Contexts == nil ||
		!slices.Equal(*m.Blockers[0].Contexts, []string{"Deploy / build"}) {
		t.Fatalf("mergeability con build rosso: %+v", m)
	}
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusConflict, "status_failed")

	// Nuova esecuzione sullo stesso sha: lo stato torna pending, poi verde.
	e.dispatch("refs/heads/feat", head)
	if got := e.blockers("app", n); !slices.Equal(got, []string{"status_required"}) {
		t.Fatalf("nuova esecuzione in corso: %v", got)
	}
	e.finish(func(string) string { return ci.ConclusionSuccess })
	if got := e.blockers("app", n); len(got) != 0 {
		t.Fatalf("con build verde: %v", got)
	}
	e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
}

// Annullamento: i job in attesa o in coda diventano error «Annullato» (non
// restano pending, che bloccherebbero il merge per sempre); chi non ha write
// non annulla (403), come per Riesegui (GIT-314).
func TestCI_AnnulloAggiornaStatiEPermessi_GIT281(t *testing.T) {
	e := newG281Env(t)
	e.hookIDOf(e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["workflow_run"],"secret":"s3cr3t-ci"}`, hookURL)))
	const sha = "4444444444444444444444444444444444444444"
	run := e.dispatch("refs/heads/main", sha)
	base := "/repos/alice/app/actions/runs/" + run.String()

	e.want(e.do(http.MethodPost, base+"/cancel", "bob", ""), http.StatusForbidden, "")
	e.want(e.do(http.MethodPost, base+"/cancel", "alice", ""), http.StatusAccepted, "")

	want := map[string]string{"Deploy / build": "error", "Deploy / lint": "error", "Deploy / test": "error"}
	e.wantStatuses("dopo l'annullamento", sha, want)
	var desc string
	if err := e.pool.QueryRow(context.Background(), `SELECT description FROM core.commit_statuses WHERE repo_id = $1 AND sha = $2 AND context = 'Deploy / lint'`,
		e.repoID, sha).Scan(&desc); err != nil || desc != "Annullato" {
		t.Fatalf("descrizione annullata: %q (%v)", desc, err)
	}
	e.run()
	found := false
	for _, r := range e.recv.requests() {
		var p struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(r.body, &p); err != nil {
			t.Fatal(err)
		}
		if r.header.Get("X-GitStack-Event") == "workflow_run" && p.Action == "cancelled" {
			verifySignature(t, "s3cr3t-ci", r)
			found = true
		}
	}
	if !found {
		t.Fatal("manca la consegna workflow_run cancelled")
	}
}
