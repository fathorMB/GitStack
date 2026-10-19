//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-265 (PR11): webhook pull_request, pull_request_review e commit_status.
// Payload conformi agli schemi del contratto (WebhookPullRequestPayload,
// WebhookPullRequestReviewPayload, WebhookCommitStatusPayload: nessun campo in
// più, nessun campo obbligatorio mancante), firmati come gli altri, con i
// tentativi di C7 e Redeliver.

const prHookSecret = "s3cr3t-delle-PR"

// byAction raccoglie le richieste ricevute per azione; le consegne partono in
// parallelo e l'ordine d'arrivo non è definito.
func (e *pevEnv) byAction(event string) map[string][]received {
	e.t.Helper()
	out := map[string][]received{}
	for _, r := range e.recv.requests() {
		if r.header.Get("X-GitStack-Event") != event {
			continue
		}
		var p struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(r.body, &p); err != nil {
			e.t.Fatal(err)
		}
		out[p.Action] = append(out[p.Action], r)
	}
	return out
}

func TestPullsWebhookTreEventiConPayloadFirmato(t *testing.T) {
	e := newPevEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "dave")
	e.branch(repoID, "feat")
	e.run() // gli eventi di creazione del repo non toccano i webhook creati dopo

	rec := e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["pull_request","pull_request_review","commit_status"],"secret":%q}`, hookURL, prHookSecret))
	e.want(rec, http.StatusCreated, "")
	hook := e.hookIDOf(rec)
	onlyStatus := e.hookIDOf(e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["commit_status"]}`, hookURL)))

	var n int64
	t.Run("pull_request_opened_e_review_requested", func(t *testing.T) {
		n = e.openPR("bob", "feat", `,"body":"Una PR","reviewers":["dave"]`)
		if got := e.run(); got < 2 {
			t.Fatalf("consegne inviate = %d, attese opened e review_requested", got)
		}
		by := e.byAction("pull_request")
		if len(by["opened"]) != 1 || len(by["review_requested"]) != 1 {
			t.Fatalf("azioni ricevute: %v", keysOfRequests(by))
		}
		for action, reqs := range by {
			req := reqs[0]
			verifySignature(t, prHookSecret, req)
			if req.header.Get("X-GitStack-Event") != "pull_request" || req.header.Get("X-GitStack-Delivery") == "" {
				t.Fatalf("intestazioni: %v", req.header)
			}
			var p openapi.WebhookPullRequestPayload
			strictDecode(t, req.body, &p)
			top := needKeys(t, action, req.body, append(hookCommonKeys, "pullRequest")...)
			needKeys(t, action+"/pullRequest", top["pullRequest"], pullFullKeys...)
			if p.Version != 1 || p.Event != "pull_request" || string(p.Action) != action || p.Sender == nil || p.Sender.Username != "bob" ||
				p.Repository.FullName != "alice/app" || p.PullRequest.Number != n || p.PullRequest.Title != "PR feat" ||
				p.PullRequest.Body != "Una PR" || p.PullRequest.State != "open" || p.PullRequest.SourceBranch != "feat" ||
				p.PullRequest.TargetBranch != "main" || p.PullRequest.Author.Username != "bob" ||
				len(p.PullRequest.RequestedReviewers) != 1 || p.PullRequest.RequestedReviewers[0].Username != "dave" ||
				p.PullRequest.CommitCount != 2 || p.PullRequest.ChangedFiles != 2 {
				t.Fatalf("payload di %s: %+v", action, p)
			}
			if action == "review_requested" {
				if p.RequestedReviewer == nil || p.RequestedReviewer.Username != "dave" || p.RequestedReviewer.Kind != "human" {
					t.Fatalf("requestedReviewer = %+v", p.RequestedReviewer)
				}
				needKeys(t, "requestedReviewer", top["requestedReviewer"], "id", "username", "kind")
			} else if _, ok := top["requestedReviewer"]; ok {
				t.Fatalf("requestedReviewer solo per le azioni di review: %s", req.body)
			}
		}
	})

	t.Run("pull_request_review_submitted", func(t *testing.T) {
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"approve","body":"ok"}`), http.StatusCreated, "")
		e.run()
		by := e.byAction("pull_request_review")
		if len(by["submitted"]) != 1 {
			t.Fatalf("review ricevute: %v", keysOfRequests(by))
		}
		req := by["submitted"][0]
		verifySignature(t, prHookSecret, req)
		var p openapi.WebhookPullRequestReviewPayload
		strictDecode(t, req.body, &p)
		top := needKeys(t, "review", req.body, append(hookCommonKeys, "pullRequest", "review")...)
		needKeys(t, "review/pullRequest", top["pullRequest"], pullSummaryKeys...)
		needKeys(t, "review/review", top["review"], reviewKeys...)
		if p.Review.State != "approved" || !p.Review.CountsAsApproval || p.Review.Stale || p.Review.Author.Username != "dave" || p.Review.Body != "ok" ||
			p.PullRequest.Number != n || p.PullRequest.ReviewDecision != "approved" || p.Sender == nil || p.Sender.Username != "dave" {
			t.Fatalf("payload della review: %+v", p)
		}
		// Un commento semplice o una richiesta di modifiche sono review inviate
		// come le altre: l'azione è sempre submitted, lo stato è nella review.
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"request_changes","body":"no"}`), http.StatusCreated, "")
		e.run()
		if got := e.byAction("pull_request_review")["submitted"]; len(got) != 2 {
			t.Fatalf("review ricevute dopo la seconda = %d", len(got))
		}
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"approve"}`), http.StatusCreated, "")
		e.run()
	})

	t.Run("commit_status_created", func(t *testing.T) {
		head := e.git.of(repoID)["feat"]
		e.want(e.postStatus("app", "bob", head, `{"state":"success","context":"ci/build","description":"ok","targetUrl":"https://ci.example.com/1"}`), http.StatusCreated, "")
		e.run()
		by := e.byAction("commit_status")
		if len(by["created"]) != 2 {
			t.Fatalf("consegne di commit_status: %d, attese 2 (due webhook)", len(by["created"]))
		}
		req := by["created"][0]
		var p openapi.WebhookCommitStatusPayload
		strictDecode(t, req.body, &p)
		top := needKeys(t, "commit_status", req.body, append(hookCommonKeys, "commitStatus", "combinedState", "branches")...)
		needKeys(t, "commit_status/commitStatus", top["commitStatus"], statusKeys...)
		if p.Version != 1 || p.Event != "commit_status" || p.Action != "created" || p.CommitStatus.Sha != head || p.CommitStatus.State != "success" ||
			p.CommitStatus.Context != "ci/build" || p.CommitStatus.Creator.Username != "bob" || p.CombinedState == nil || *p.CombinedState != "success" ||
			p.Branches == nil || !slices.Equal(*p.Branches, []string{"feat"}) || p.Sender == nil || p.Sender.Username != "bob" {
			t.Fatalf("payload dello stato: %+v", p)
		}
		// Il webhook completo ha tutte le consegne finora: opened, review_requested, 3 review e lo stato.
		if got := len(e.deliveries("app", hook)); got != 6 {
			t.Fatalf("consegne del webhook completo = %d, attese 6", got)
		}
		// Il secondo webhook (solo commit_status) l'ha ricevuto; i pull_request no.
		if len(e.deliveries("app", onlyStatus)) != 1 {
			t.Fatalf("consegne del webhook solo-stato = %d", len(e.deliveries("app", onlyStatus)))
		}
		for _, r := range e.recv.requests() {
			verifyAny(t, r)
		}
	})

	t.Run("synchronize_e_approvazione_annullata_da_nuovi_commit", func(t *testing.T) {
		before := e.git.of(repoID)["feat"]
		after := sha(4242)
		e.git.setBranch(repoID, "feat", after)
		e.pushed(repoID, "feat", before, after)
		e.run()
		syncs := e.byAction("pull_request")["synchronize"]
		dismissed := e.byAction("pull_request_review")["dismissed"]
		if len(syncs) != 1 || len(dismissed) != 1 {
			t.Fatalf("synchronize=%d dismissed=%d (attesi 1 e 1: l'ultima approvazione di dave)", len(syncs), len(dismissed))
		}
		var ps openapi.WebhookPullRequestPayload
		strictDecode(t, syncs[0].body, &ps)
		if ps.PullRequest.HeadSha != after || ps.PullRequest.Approvals != 0 || ps.Sender == nil || ps.Sender.Username != "bob" {
			t.Fatalf("synchronize: %+v", ps.PullRequest)
		}
		var pd openapi.WebhookPullRequestReviewPayload
		strictDecode(t, dismissed[0].body, &pd)
		needKeys(t, "dismissed", dismissed[0].body, append(hookCommonKeys, "pullRequest", "review")...)
		if pd.Action != "dismissed" || !pd.Review.Stale || pd.Review.CountsAsApproval || pd.Review.State != "approved" || pd.Review.Author.Username != "dave" {
			t.Fatalf("dismissed: %+v", pd.Review)
		}
		// Lo stesso push consegnato due volte non ripete niente.
		e.pushed(repoID, "feat", before, after)
		e.run()
		if got := len(e.byAction("pull_request")["synchronize"]); got != 1 {
			t.Fatalf("synchronize ripetuto: %d", got)
		}
	})

	t.Run("modifica_pronta_bozza_chiusa_riaperta_unita", func(t *testing.T) {
		e.want(e.do(http.MethodPatch, e.pull(n), "bob", `{"title":"Nuovo titolo","body":"Nuovo testo"}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, e.pull(n)+"/draft", "bob", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPost, e.pull(n)+"/ready", "bob", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPost, e.pull(n)+"/close", "bob", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPost, e.pull(n)+"/reopen", "bob", ""), http.StatusOK, "")
		e.want(e.do(http.MethodDelete, e.pull(n)+"/requested-reviewers/dave", "bob", ""), http.StatusNotFound, "")
		e.want(e.merge("app", n, "alice", `{"method":"squash"}`), http.StatusOK, "")
		e.run()
		by := e.byAction("pull_request")
		for _, a := range []string{"edited", "converted_to_draft", "ready_for_review", "closed", "reopened", "merged"} {
			if len(by[a]) != 1 {
				t.Fatalf("azione %s ricevuta %d volte: %v", a, len(by[a]), keysOfRequests(by))
			}
			var p openapi.WebhookPullRequestPayload
			strictDecode(t, by[a][0].body, &p)
			needKeys(t, a, by[a][0].body, append(hookCommonKeys, "pullRequest")...)
		}
		var edited, merged, closed openapi.WebhookPullRequestPayload
		strictDecode(t, by["edited"][0].body, &edited)
		strictDecode(t, by["merged"][0].body, &merged)
		strictDecode(t, by["closed"][0].body, &closed)
		if edited.PullRequest.Title != "Nuovo titolo" || edited.PullRequest.Body != "Nuovo testo" {
			t.Fatalf("edited: %+v", edited.PullRequest)
		}
		if merged.PullRequest.State != "merged" || merged.PullRequest.MergeCommitSha == nil || merged.PullRequest.MergedBy == nil ||
			merged.PullRequest.MergedBy.Username != "alice" || merged.Sender == nil || merged.Sender.Username != "alice" {
			t.Fatalf("merged: %+v", merged.PullRequest)
		}
		if closed.PullRequest.State != "closed" || closed.PullRequest.ClosedAt == nil {
			t.Fatalf("closed: %+v", closed.PullRequest)
		}
	})

	t.Run("nessun_webhook_per_i_commenti_e_per_gli_eventi_di_sola_notifica", func(t *testing.T) {
		before := len(e.recv.requests())
		e.branch(repoID, "other")
		m := e.openPR("bob", "other", "")
		e.want(e.do(http.MethodPost, e.pull(m)+"/comments", "dave", `{"body":"solo un commento"}`), http.StatusCreated, "")
		e.run()
		// Solo l'apertura di m: il commento generale non ha un webhook.
		if got := len(e.recv.requests()) - before; got != 1 {
			t.Fatalf("richieste dopo apertura e commento = %d, attesa 1", got)
		}
	})
}

func keysOfRequests(m map[string][]received) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		out[k] = len(v)
	}
	return out
}

// verifyAny controlla la firma delle consegne dei due webhook: il primo ha il
// segreto di prova, il secondo non ne ha (nessuna firma).
func verifyAny(t *testing.T, r received) {
	t.Helper()
	if sig := r.header.Get("X-GitStack-Signature"); sig != "" {
		verifySignature(t, prHookSecret, r)
	}
}

// I tentativi (C7) e la Redeliver valgono per i tre nuovi eventi come per gli altri.
func TestPullsWebhookTentativiERedeliver(t *testing.T) {
	e := newPevEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "dave")
	e.branch(repoID, "feat")
	e.run()
	hook := e.hookIDOf(e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["pull_request","pull_request_review","commit_status"],"secret":"uno"}`, hookURL)))

	e.recv.status(http.StatusInternalServerError)
	start := e.clock.Now()
	n := e.openPR("bob", "feat", "")
	head := e.git.of(repoID)["feat"]
	e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"comment","body":"letta"}`), http.StatusCreated, "")
	e.want(e.postStatus("app", "bob", head, `{"state":"failure"}`), http.StatusCreated, "")

	// Tre eventi, tre consegne: tutte falliscono al primo tentativo e restano in
	// coda con l'attesa di un minuto (C7), poi riescono al secondo.
	if got := e.run(); got != 3 {
		t.Fatalf("primi tentativi = %d, attesi 3", got)
	}
	ds := e.deliveries("app", hook)
	if len(ds) != 3 {
		t.Fatalf("consegne = %d", len(ds))
	}
	events := map[string]bool{}
	for _, d := range ds {
		events[string(d.Event)] = true
		if d.Status != "pending" || d.Attempt != 1 || d.StatusCode == nil || *d.StatusCode != 500 || d.NextAttemptAt == nil ||
			d.NextAttemptAt.Sub(e.clock.Now()) != time.Minute {
			t.Fatalf("dopo il primo tentativo: %+v", d)
		}
	}
	if !events["pull_request"] || !events["pull_request_review"] || !events["commit_status"] {
		t.Fatalf("eventi consegnati: %v", events)
	}
	e.clock.Advance(time.Minute - time.Second)
	if got := e.run(); got != 0 {
		t.Fatalf("ritentate prima della scadenza: %d", got)
	}
	e.recv.mu.Lock()
	e.recv.answer = nil
	e.recv.mu.Unlock()
	e.clock.Advance(time.Second)
	if got := e.run(); got != 3 {
		t.Fatalf("secondi tentativi = %d, attesi 3", got)
	}
	var first openapiDelivery
	for _, d := range e.deliveries("app", hook) {
		if d.Status != "success" || d.Attempt != 2 {
			t.Fatalf("dopo il secondo tentativo: %+v", d)
		}
		if d.Event == "pull_request" {
			first = openapiDelivery{id: d.Id.String()}
		}
	}
	if total := e.clock.Now().Sub(start); total != time.Minute {
		t.Fatalf("durata = %v", total)
	}

	// Redeliver: nuova consegna con lo stesso payload, firmata, con l'evento giusto.
	before := len(e.recv.requests())
	rec := e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/deliveries/"+first.id+"/redeliver", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	if e.run() != 1 {
		t.Fatal("la Redeliver non è partita")
	}
	reqs := e.recv.requests()
	if len(reqs) != before+1 {
		t.Fatalf("richieste dopo la Redeliver = %d, attese %d", len(reqs), before+1)
	}
	last := reqs[len(reqs)-1]
	var orig received
	for _, r := range reqs[:before] {
		if r.header.Get("X-GitStack-Delivery") == first.id {
			orig = r
		}
	}
	if string(last.body) != string(orig.body) || last.header.Get("X-GitStack-Event") != "pull_request" ||
		last.header.Get("X-GitStack-Delivery") == first.id {
		t.Fatalf("Redeliver: evento %q, delivery %q, payload uguale: %v", last.header.Get("X-GitStack-Event"),
			last.header.Get("X-GitStack-Delivery"), string(last.body) == string(orig.body))
	}
	verifySignature(t, "uno", last)
}

type openapiDelivery struct{ id string }
