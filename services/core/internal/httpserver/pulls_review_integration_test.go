//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// GIT-262 (v1.1): review e commenti sulle righe, regole PR2, PR5 e PR12c.
// Il diff della PR del servizio git finto ha a.txt (hunk -1,3 +1,4) e b.txt
// (hunk -0,0 +1,2). Utenti: alice admin del repo, bob write (autore delle
// PR), carol solo read, bot è un agente con write.

type reviewEnv struct {
	*pullsEnv
	repo uuid.UUID
}

func newReviewEnv(t *testing.T) *reviewEnv {
	t.Helper()
	e := &reviewEnv{pullsEnv: newPullsEnv(t)}
	users["bot"] = botID
	e.repo = e.pullsEnv.repo("app", true)
	e.id.grantWrite(e.repo, "bob")
	e.id.grantWrite(e.repo, "bot")
	e.id.fakeIdentity.owners["bot"] = ownerUser(botID, "bot")
	return e
}

func (e *reviewEnv) newPR(branch, author string) int64 {
	e.t.Helper()
	e.branch(e.repo, branch)
	return e.pullsEnv.open("app", author, branch)
}

func (e *reviewEnv) head(branch string) string { return e.git.of(e.repo)[branch] }

func (e *reviewEnv) review(n int64, user, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/reviews", n), user, body)
}

func (e *reviewEnv) decodeReview(rec *httptest.ResponseRecorder) openapi.PullRequestReview {
	e.t.Helper()
	var x openapi.PullRequestReview
	if err := json.Unmarshal(rec.Body.Bytes(), &x); err != nil {
		e.t.Fatalf("review non valida: %v: %s", err, rec.Body.String())
	}
	return x
}

func (e *reviewEnv) reviews(n int64) []openapi.PullRequestReview {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/reviews", n), "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestReviewList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func (e *reviewEnv) threads(n int64, query string) []openapi.PullRequestThread {
	e.t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/threads%s", n, query), "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestThreadList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func (e *reviewEnv) decision(n int64) (openapi.ReviewDecision, openapi.PullRequest) {
	e.t.Helper()
	x := e.pr(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d", n), "carol", ""))
	return x.ReviewDecision, x
}

func TestReview_PR2_PersonaAgentePropriaPRePermessi(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")

	t.Run("chi_legge_commenta_ma_non_approva", func(t *testing.T) {
		rv := e.decodeReview(e.review(n, "carol", `{"event":"comment","body":"mi pare ok"}`))
		e.want(e.review(n, "carol", `{"event":"comment","body":"mi pare ok"}`), http.StatusCreated, "")
		if rv.State != openapi.ReviewStateCommented || rv.Author.Username != "carol" || rv.CountsAsApproval || rv.Stale || rv.CommitSha != e.head("feat") {
			t.Fatalf("review = %+v", rv)
		}
		e.want(e.review(n, "carol", `{"event":"approve"}`), http.StatusForbidden, "forbidden")
		e.want(e.review(n, "carol", `{"event":"request_changes","body":"no"}`), http.StatusForbidden, "forbidden")
	})

	t.Run("la_propria_PR_non_si_approva_ne_si_blocca", func(t *testing.T) {
		e.want(e.review(n, "bob", `{"event":"approve"}`), http.StatusForbidden, "own_pull_request")
		e.want(e.review(n, "bob", `{"event":"request_changes","body":"x"}`), http.StatusForbidden, "own_pull_request")
		// commentare la propria PR si può
		e.want(e.review(n, "bob", `{"event":"comment","body":"nota"}`), http.StatusCreated, "")
	})

	t.Run("input_non_validi", func(t *testing.T) {
		e.want(e.review(n, "alice", `{"event":"boh"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.review(n, "alice", `{"event":"request_changes"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.review(n, "alice", `{"event":"comment"}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.review(n, "alice", fmt.Sprintf(`{"event":"comment","body":%q}`, strings.Repeat("x", 65537))), http.StatusRequestEntityTooLarge, "body_too_large")
		e.want(e.review(n, "alice", `{"event":"approve","commitSha":"`+strings.Repeat("a", 40)+`"}`), http.StatusConflict, "head_changed")
	})

	t.Run("agente_approva_ma_non_conta", func(t *testing.T) {
		rv := e.decodeReview(e.review(n, "bot", `{"event":"approve","body":"LGTM"}`))
		if rv.State != openapi.ReviewStateApproved || rv.CountsAsApproval || rv.Author.Kind != "agent" {
			t.Fatalf("approvazione dell'agente = %+v", rv)
		}
		if d, x := e.decision(n); d != "none" || x.Approvals != 0 {
			t.Fatalf("decisione con la sola approvazione dell'agente = %s, approvazioni %d", d, x.Approvals)
		}
		// nemmeno la richiesta di modifiche di un agente blocca
		e.want(e.review(n, "bot", `{"event":"request_changes","body":"mah"}`), http.StatusCreated, "")
		if d, _ := e.decision(n); d != "none" {
			t.Fatalf("decisione con la richiesta di modifiche di un agente = %s", d)
		}
	})

	t.Run("la_persona_con_write_approva_e_conta", func(t *testing.T) {
		rv := e.decodeReview(e.review(n, "alice", `{"event":"approve"}`))
		if !rv.CountsAsApproval || rv.Author.Username != "alice" {
			t.Fatalf("approvazione = %+v", rv)
		}
		d, x := e.decision(n)
		if d != "approved" || x.Approvals != 1 {
			t.Fatalf("decisione = %s, approvazioni %d", d, x.Approvals)
		}
		var counted, shown int
		for _, r := range e.reviews(n) {
			shown++
			if r.CountsAsApproval {
				counted++
			}
		}
		if counted != 1 || shown != 6 {
			t.Fatalf("review elencate %d, che contano %d", shown, counted)
		}
	})

	t.Run("richiesta_di_modifiche_e_nuova_approvazione", func(t *testing.T) {
		e.want(e.review(n, "alice", `{"event":"request_changes","body":"manca un test"}`), http.StatusCreated, "")
		if d, _ := e.decision(n); d != "changes_requested" {
			t.Fatalf("decisione = %s", d)
		}
		// la vecchia approvazione non conta più: l'ultima review di alice è la richiesta
		for _, r := range e.reviews(n) {
			if r.CountsAsApproval {
				t.Fatalf("approvazione superata ancora valida: %+v", r)
			}
		}
		e.want(e.review(n, "alice", `{"event":"approve"}`), http.StatusCreated, "")
		if d, _ := e.decision(n); d != "approved" {
			t.Fatalf("decisione dopo la nuova approvazione = %s", d)
		}
	})

	t.Run("nuovi_commit_annullano_l_approvazione", func(t *testing.T) {
		e.git.setBranch(e.repo, "feat", sha(999))
		d, x := e.decision(n)
		if d != "none" || x.Approvals != 0 {
			t.Fatalf("decisione dopo nuovi commit = %s, approvazioni %d", d, x.Approvals)
		}
		stale := 0
		for _, r := range e.reviews(n) {
			if r.State == openapi.ReviewStateApproved && r.Stale && !r.CountsAsApproval {
				stale++
			}
		}
		if stale != 3 {
			t.Fatalf("approvazioni annullate = %d, volute 3 (alice x2, bot)", stale)
		}
	})
}

func TestReview_PR2_BozzaChiusaERepoArchiviato(t *testing.T) {
	e := newReviewEnv(t)
	e.branch(e.repo, "wip")
	rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"wip","sourceBranch":"wip","draft":true}`)
	e.want(rec, http.StatusCreated, "")
	n := e.pr(rec).Number
	e.want(e.review(n, "alice", `{"event":"approve"}`), http.StatusConflict, "draft")
	e.want(e.review(n, "alice", `{"event":"request_changes","body":"x"}`), http.StatusConflict, "draft")
	e.want(e.review(n, "alice", `{"event":"comment","body":"in corso?"}`), http.StatusCreated, "")

	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", n), "bob", ""), http.StatusOK, "")
	e.want(e.review(n, "alice", `{"event":"comment","body":"tardi"}`), http.StatusConflict, "not_open")
	e.want(e.review(n, "alice", `{"event":"approve"}`), http.StatusConflict, "not_open")

	e.want(e.review(999, "alice", `{"event":"comment","body":"x"}`), http.StatusNotFound, "not_found")
	secret := e.pullsEnv.repo("secret", false)
	e.branch(secret, "feat")
	e.want(e.do(http.MethodPost, "/repos/alice/secret/pulls/1/reviews", "carol", `{"event":"comment","body":"x"}`), http.StatusNotFound, "not_found")

	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repo)
	e.want(e.review(n, "alice", `{"event":"comment","body":"x"}`), http.StatusConflict, "archived")
}

func TestReview_StatoContaSoloLePersone(t *testing.T) {
	e := newReviewEnv(t)
	e.id.grantWrite(e.repo, "carol") // da ora carol ha write
	e.sql(`INSERT INTO core.branch_settings (repo_id, require_pull_request, required_approvals) VALUES ($1, true, 2)`, e.repo)
	n := e.newPR("feat", "bob")

	if d, x := e.decision(n); d != "required" || x.RequiredApprovals != 2 || x.Approvals != 0 {
		t.Fatalf("senza review: %s %d/%d", d, x.Approvals, x.RequiredApprovals)
	}
	e.want(e.review(n, "bot", `{"event":"approve"}`), http.StatusCreated, "")
	e.want(e.review(n, "alice", `{"event":"approve"}`), http.StatusCreated, "")
	// un agente e una persona: 1 su 2, l'agente non si somma
	if d, x := e.decision(n); d != "required" || x.Approvals != 1 {
		t.Fatalf("agente + persona: %s approvazioni %d", d, x.Approvals)
	}
	e.want(e.review(n, "carol", `{"event":"approve"}`), http.StatusCreated, "")
	if d, x := e.decision(n); d != "approved" || x.Approvals != 2 {
		t.Fatalf("due persone: %s approvazioni %d", d, x.Approvals)
	}
	e.want(e.review(n, "carol", `{"event":"request_changes","body":"ripensamento"}`), http.StatusCreated, "")
	if d, x := e.decision(n); d != "changes_requested" || x.Approvals != 1 {
		t.Fatalf("modifiche richieste: %s approvazioni %d", d, x.Approvals)
	}
	// l'elenco delle PR ha la stessa decisione
	rec := e.do(http.MethodGet, "/repos/alice/app/pulls", "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Items) != 1 || l.Items[0].ReviewDecision != "changes_requested" {
		t.Fatalf("elenco = %+v", l.Items)
	}
}

func TestReview_PR5_CommentiSulleRigheEConversazioni(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")
	head := e.head("feat")

	t.Run("righe_fuori_dal_diff_o_gruppo_non_valido", func(t *testing.T) {
		bad := func(c string, code string) {
			t.Helper()
			e.want(e.review(n, "alice", `{"event":"comment","comments":[`+c+`]}`), http.StatusUnprocessableEntity, code)
		}
		bad(`{"path":"a.txt","side":"new","line":50,"body":"x"}`, "line_not_in_diff")
		bad(`{"path":"zzz.txt","side":"new","line":1,"body":"x"}`, "line_not_in_diff")
		bad(`{"path":"b.txt","side":"old","line":1,"body":"x"}`, "line_not_in_diff") // b.txt non ha righe vecchie
		bad(`{"path":"a.txt","side":"new","startLine":3,"line":3,"body":"x"}`, "validation_failed")
		bad(`{"path":"a.txt","side":"new","startLine":1,"line":9,"body":"x"}`, "line_not_in_diff")
		bad(`{"path":"a.txt","side":"new","line":1,"body":""}`, "validation_failed")
	})

	var rid string
	t.Run("review_con_commento_su_riga_e_su_gruppo", func(t *testing.T) {
		sugg := "```suggestion\\nlet x = 2\\n```"
		rec := e.review(n, "alice", `{"event":"request_changes","body":"vedi i commenti","commitSha":"`+head+`","comments":[
			{"path":"a.txt","side":"new","line":2,"body":"`+sugg+`"},
			{"path":"a.txt","side":"new","startLine":1,"line":4,"body":"tutto il blocco"},
			{"path":"a.txt","side":"old","line":3,"body":"riga vecchia"}]}`)
		e.want(rec, http.StatusCreated, "")
		rv := e.decodeReview(rec)
		rid = rv.Id.String()
		if rv.CommentCount != 3 || rv.State != openapi.ReviewStateChangesRequested {
			t.Fatalf("review = %+v", rv)
		}
		ths := e.threads(n, "")
		if len(ths) != 3 {
			t.Fatalf("conversazioni = %d", len(ths))
		}
		first := ths[0]
		if first.Path != "a.txt" || first.Line != 2 || first.Side != openapi.New || first.Resolved || first.Outdated || first.StartLine != nil {
			t.Fatalf("conversazione = %+v", first)
		}
		// il blocco suggestion arriva com'è: la UI lo mostra come diff, senza «Applica»
		if len(first.Comments) != 1 || !strings.Contains(first.Comments[0].Body, "```suggestion") || first.Comments[0].Id != first.Id ||
			first.Comments[0].ReviewId == nil || first.Comments[0].ReviewId.String() != rid || *first.Comments[0].CommitSha != head {
			t.Fatalf("commento = %+v", first.Comments)
		}
		if ths[1].StartLine == nil || *ths[1].StartLine != 1 || ths[1].Line != 4 {
			t.Fatalf("gruppo di righe = %+v", ths[1])
		}
		if ths[2].Side != openapi.Old || ths[2].Line != 3 {
			t.Fatalf("lato vecchio = %+v", ths[2])
		}
		_, x := e.decision(n)
		if x.CommentCount != 3 || x.UnresolvedThreadCount != 3 {
			t.Fatalf("contatori della PR = %d commenti, %d aperte", x.CommentCount, x.UnresolvedThreadCount)
		}
	})

	t.Run("risposta_e_commento_singolo", func(t *testing.T) {
		first := e.threads(n, "")[0]
		rec := e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "bob", fmt.Sprintf(`{"body":"fatto","inReplyTo":%q}`, first.Id))
		e.want(rec, http.StatusCreated, "")
		var c openapi.PullRequestComment
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		if c.ThreadId == nil || *c.ThreadId != first.Id || c.Author.Username != "bob" || c.ReviewId != nil {
			t.Fatalf("risposta = %+v", c)
		}
		if ths := e.threads(n, ""); len(ths[0].Comments) != 2 || ths[0].Comments[1].Body != "fatto" {
			t.Fatalf("conversazione = %+v", ths[0].Comments)
		}
		// commento su una riga di b.txt, da chi ha solo read
		rec = e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
			fmt.Sprintf(`{"body":"domanda","path":"b.txt","side":"new","line":2,"commitSha":%q}`, head))
		e.want(rec, http.StatusCreated, "")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
			fmt.Sprintf(`{"body":"x","path":"b.txt","side":"new","line":7,"commitSha":%q}`, head)), http.StatusUnprocessableEntity, "line_not_in_diff")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
			fmt.Sprintf(`{"body":"x","path":"b.txt","side":"new","line":1,"commitSha":%q}`, strings.Repeat("c", 40))), http.StatusConflict, "head_changed")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
			fmt.Sprintf(`{"body":"x","inReplyTo":%q}`, uuid.New())), http.StatusNotFound, "not_found")
		// elenco piatto: 3 + risposta + commento su b.txt
		rec = e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.PullRequestCommentList
		_ = json.Unmarshal(rec.Body.Bytes(), &l)
		if l.Total != 5 || len(l.Items) != 5 {
			t.Fatalf("commenti = %d/%d", len(l.Items), l.Total)
		}
	})

	t.Run("modifica_del_commento_solo_all_autore", func(t *testing.T) {
		first := e.threads(n, "")[0]
		path := fmt.Sprintf("/repos/alice/app/pulls/%d/comments/%s", n, first.Id)
		e.want(e.do(http.MethodPatch, path, "bob", `{"body":"x"}`), http.StatusForbidden, "forbidden")
		rec := e.do(http.MethodPatch, path, "alice", `{"body":"modificato"}`)
		e.want(rec, http.StatusOK, "")
		var c openapi.PullRequestComment
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		if !c.Edited || c.Body != "modificato" {
			t.Fatalf("commento = %+v", c)
		}
	})

	t.Run("risolta_e_riaperta_da_autore_o_write", func(t *testing.T) {
		ths := e.threads(n, "")
		path := func(id openapi_uuid, op string) string {
			return fmt.Sprintf("/repos/alice/app/pulls/%d/threads/%s/%s", n, id, op)
		}
		// chi ha solo read non risolve
		e.want(e.do(http.MethodPost, path(ths[0].Id, "resolve"), "carol", ""), http.StatusForbidden, "forbidden")
		// l'autore della PR (bob) risolve
		rec := e.do(http.MethodPost, path(ths[0].Id, "resolve"), "bob", "")
		e.want(rec, http.StatusOK, "")
		var th openapi.PullRequestThread
		_ = json.Unmarshal(rec.Body.Bytes(), &th)
		if !th.Resolved || th.ResolvedBy == nil || th.ResolvedBy.Username != "bob" || th.ResolvedAt == nil {
			t.Fatalf("conversazione risolta = %+v", th)
		}
		// idempotente
		e.want(e.do(http.MethodPost, path(ths[0].Id, "resolve"), "bob", ""), http.StatusOK, "")
		// chi ha write (non autore) risolve un'altra
		e.want(e.do(http.MethodPost, path(ths[1].Id, "resolve"), "alice", ""), http.StatusOK, "")
		if got := len(e.threads(n, "?resolved=true")); got != 2 {
			t.Fatalf("risolte = %d", got)
		}
		if got := len(e.threads(n, "?resolved=false")); got != 2 {
			t.Fatalf("aperte = %d", got)
		}
		if _, x := e.decision(n); x.UnresolvedThreadCount != 2 {
			t.Fatalf("aperte nella PR = %d", x.UnresolvedThreadCount)
		}
		// riapertura: carol no, alice sì
		e.want(e.do(http.MethodPost, path(ths[0].Id, "unresolve"), "carol", ""), http.StatusForbidden, "forbidden")
		rec = e.do(http.MethodPost, path(ths[0].Id, "unresolve"), "alice", "")
		e.want(rec, http.StatusOK, "")
		th = openapi.PullRequestThread{}
		_ = json.Unmarshal(rec.Body.Bytes(), &th)
		if th.Resolved || th.ResolvedBy != nil || th.ResolvedAt != nil {
			t.Fatalf("conversazione riaperta = %+v", th)
		}
		e.want(e.do(http.MethodPost, path(ths[0].Id, "unresolve"), "alice", ""), http.StatusOK, "")
		e.want(e.do(http.MethodPost, path(uuid.New(), "resolve"), "alice", ""), http.StatusNotFound, "not_found")
	})
}

// openapi_uuid evita di importare il pacchetto dei tipi solo per l'alias.
type openapi_uuid = uuid.UUID

func TestReview_PR5_OutdatedDopoNuoviCommit(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")
	head := e.head("feat")
	e.want(e.review(n, "alice", `{"event":"comment","comments":[
		{"path":"a.txt","side":"new","line":1,"body":"riga 1"},
		{"path":"a.txt","side":"new","line":3,"body":"riga 3"},
		{"path":"a.txt","side":"old","line":2,"body":"lato vecchio"},
		{"path":"b.txt","side":"new","line":1,"body":"altro file"}]}`), http.StatusCreated, "")

	outdated := func() map[string]bool {
		out := map[string]bool{}
		for _, th := range e.threads(n, "") {
			out[fmt.Sprintf("%s:%s:%d", th.Path, th.Side, th.Line)] = th.Outdated
		}
		return out
	}
	// senza nuovi commit niente è outdated
	for k, v := range outdated() {
		if v {
			t.Fatalf("%s outdated senza nuovi commit", k)
		}
	}

	// nuovo commit che cambia solo la riga 3 di a.txt (hunk -3,1 +3,1)
	e.git.setBranch(e.repo, "feat", sha(4242))
	e.git.setSince(`[{"path":"a.txt","status":"modified","additions":1,"deletions":1,"binary":false,"truncated":false,"patch":"@@ -3,1 +3,1 @@\n-c\n+z\n"}]`)
	got := outdated()
	if !got["a.txt:new:3"] || got["a.txt:new:1"] || got["a.txt:old:2"] || got["b.txt:new:1"] {
		t.Fatalf("outdated dopo il commit su a.txt:3 = %v", got)
	}
	// i commenti seguono la conversazione
	for _, th := range e.threads(n, "") {
		for _, c := range th.Comments {
			if c.Outdated != th.Outdated {
				t.Fatalf("commento %+v non allineato alla conversazione %v", c, th.Outdated)
			}
		}
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestCommentList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	nOut := 0
	for _, c := range l.Items {
		if c.Outdated {
			nOut++
		}
	}
	if nOut != 1 {
		t.Fatalf("commenti outdated nell'elenco = %d", nOut)
	}

	// lo stato non torna indietro, anche se l'ultimo confronto non tocca più niente
	e.git.setSince("")
	if !outdated()["a.txt:new:3"] {
		t.Fatalf("outdated non persistente")
	}

	// un commento sul commit vecchio ora è 409; con la nuova punta si può
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
		fmt.Sprintf(`{"body":"x","path":"a.txt","side":"new","line":1,"commitSha":%q}`, head)), http.StatusConflict, "head_changed")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n), "carol",
		fmt.Sprintf(`{"body":"ok","path":"a.txt","side":"new","line":1,"commitSha":%q}`, sha(4242))), http.StatusCreated, "")

	// un file tolto dal nuovo commit rende outdated anche gli altri commenti su di esso
	e.git.setBranch(e.repo, "feat", sha(4343))
	e.git.setSince(`[{"path":"b.txt","status":"deleted","additions":0,"deletions":2,"binary":false,"truncated":false}]`)
	if !outdated()["b.txt:new:1"] {
		t.Fatalf("file eliminato: il commento deve diventare outdated")
	}
}

func TestReview_PR12c_RichiestaDiReview(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")
	req := func(user, body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/requested-reviewers", n), user, body)
	}
	names := func(x openapi.PullRequest) string {
		var out []string
		for _, u := range x.RequestedReviewers {
			out = append(out, u.Username)
		}
		return strings.Join(out, ",")
	}

	t.Run("chi_puo_chiedere", func(t *testing.T) {
		// chi ha solo read non chiede
		e.want(req("carol", `{"reviewers":["alice"]}`), http.StatusForbidden, "forbidden")
		// l'autore chiede a chi ha almeno read (carol ha read)
		rec := req("bob", `{"reviewers":["carol"]}`)
		e.want(rec, http.StatusOK, "")
		x := e.pr(rec)
		if names(x) != "carol" || x.ReviewDecision != "required" {
			t.Fatalf("revisori = %s decisione %s", names(x), x.ReviewDecision)
		}
		// chi ha write (non autore) chiede, anche a un agente; doppioni ignorati
		x = e.pr(req("alice", `{"reviewers":["bot","carol","bot"]}`))
		if names(x) != "carol,bot" && names(x) != "bot,carol" {
			t.Fatalf("revisori = %s", names(x))
		}
	})

	t.Run("a_chi", func(t *testing.T) {
		secret := e.pullsEnv.repo("secret", false)
		e.branch(secret, "feat")
		sn := e.pullsEnv.open("secret", "alice", "feat")
		e.id.grantWrite(secret, "bob")
		sreq := func(body string) *httptest.ResponseRecorder {
			return e.do(http.MethodPost, fmt.Sprintf("/repos/alice/secret/pulls/%d/requested-reviewers", sn), "bob", body)
		}
		// carol non legge il repo privato
		rec := sreq(`{"reviewers":["carol"]}`)
		e.want(rec, http.StatusUnprocessableEntity, "reviewer_no_access")
		if !strings.Contains(rec.Body.String(), `"reviewers"`) {
			t.Fatalf("manca details.fields.reviewers: %s", rec.Body.String())
		}
		// l'autore della PR non può essere revisore
		e.want(req("alice", `{"reviewers":["bob"]}`), http.StatusUnprocessableEntity, "validation_failed")
		// utente inesistente, organizzazione, lista vuota o troppo lunga
		e.want(req("alice", `{"reviewers":["nessuno"]}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(req("alice", `{"reviewers":["acme"]}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(req("alice", `{"reviewers":[]}`), http.StatusUnprocessableEntity, "validation_failed")
		many := make([]string, 11)
		for i := range many {
			many[i] = fmt.Sprintf("u%d", i)
		}
		b, _ := json.Marshal(map[string]any{"reviewers": many})
		e.want(req("alice", string(b)), http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("nuova_richiesta_dopo_la_review", func(t *testing.T) {
		e.want(e.review(n, "carol", `{"event":"comment","body":"visto"}`), http.StatusCreated, "")
		x := e.pr(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/app/pulls/%d", n), "carol", ""))
		if strings.Contains(names(x), "carol") {
			t.Fatalf("chi ha recensito resta fra i richiesti: %s", names(x))
		}
		x = e.pr(req("bob", `{"reviewers":["carol"]}`))
		if !strings.Contains(names(x), "carol") {
			t.Fatalf("nuova richiesta non registrata: %s", names(x))
		}
	})

	t.Run("togliere_la_richiesta", func(t *testing.T) {
		del := func(user, who string) *httptest.ResponseRecorder {
			return e.do(http.MethodDelete, fmt.Sprintf("/repos/alice/app/pulls/%d/requested-reviewers/%s", n, who), user, "")
		}
		e.want(del("carol", "bot"), http.StatusForbidden, "forbidden")
		x := e.pr(del("bob", "bot"))
		if names(x) != "carol" {
			t.Fatalf("revisori = %s", names(x))
		}
		e.want(del("bob", "bot"), http.StatusNotFound, "not_found")
		e.want(del("bob", "alice"), http.StatusNotFound, "not_found")
		e.want(del("alice", "nessuno"), http.StatusNotFound, "not_found")
		x = e.pr(del("alice", "carol"))
		if len(x.RequestedReviewers) != 0 {
			t.Fatalf("revisori = %s", names(x))
		}
	})

	t.Run("bozza_chiusa_e_archiviato", func(t *testing.T) {
		e.branch(e.repo, "wip")
		rec := e.do(http.MethodPost, "/repos/alice/app/pulls", "bob", `{"title":"wip","sourceBranch":"wip","draft":true}`)
		e.want(rec, http.StatusCreated, "")
		dn := e.pr(rec).Number
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/requested-reviewers", dn), "bob", `{"reviewers":["carol"]}`), http.StatusConflict, "draft")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", n), "bob", ""), http.StatusOK, "")
		e.want(req("bob", `{"reviewers":["carol"]}`), http.StatusConflict, "not_open")
		e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repo)
		e.want(req("bob", `{"reviewers":["carol"]}`), http.StatusConflict, "archived")
	})
}
