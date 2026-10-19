//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// GIT-265 (PR8): le parole chiave di C2 nella descrizione di una PR collegano
// la issue («PR #n will close this issue»); al merge nel branch principale,
// con qualsiasi metodo, la issue si chiude come completed. Chiusa senza merge,
// la PR non chiude niente.

func (e *pevEnv) newIssue(repo, title string) int64 {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/issues", "alice", fmt.Sprintf(`{"title":%q,"body":"x"}`, title))
	e.want(rec, http.StatusCreated, "")
	return e.issue(rec).Number
}

func (e *pevEnv) issueState(repo string, n int64) (state, reason string) {
	e.t.Helper()
	var r *string
	if err := e.pool.QueryRow(context.Background(), `SELECT i.state, i.close_reason FROM core.issues i
		JOIN core.repositories r ON r.resource_id = i.repo_id WHERE r.name = $1 AND i.number = $2`, repo, n).Scan(&state, &r); err != nil {
		e.t.Fatal(err)
	}
	if r != nil {
		reason = *r
	}
	return state, reason
}

func (e *pevEnv) closing(n int64) []int64 {
	e.t.Helper()
	rec := e.do(http.MethodGet, e.pull(n), "alice", "")
	e.want(rec, http.StatusOK, "")
	return e.pr(rec).ClosingIssues
}

// willClose: l'evento referenced_from della issue che dice «questa PR la chiude».
func (e *pevEnv) willClose(repo string, issue, pr int64) bool {
	e.t.Helper()
	for _, ev := range e.events(repo, issue, "alice") {
		if ev.Type != "referenced_from" || ev.Data == nil {
			continue
		}
		src, _ := (*ev.Data)["source"].(map[string]any)
		if wc, _ := (*ev.Data)["willClose"].(bool); wc && src["kind"] == "pull_request" && src["number"] == float64(pr) {
			return true
		}
	}
	return false
}

func TestPullsChiusuraDelleIssueAlMerge(t *testing.T) {
	e := newPevEnv(t)
	repoID := e.repo("app", true)
	otherID := e.repo("other", true)
	e.id.grantWrite(repoID, "bob")
	for _, b := range []string{"feat", "fix", "nope", "edit", "cross1", "cross2", "side"} {
		e.branch(repoID, b)
	}
	i1, i2, i3, i4, i5 := e.newIssue("app", "uno"), e.newIssue("app", "due"), e.newIssue("app", "tre"), e.newIssue("app", "quattro"), e.newIssue("app", "cinque")
	o1, o2 := e.newIssue("other", "o1"), e.newIssue("other", "o2")

	t.Run("le_parole_chiave_collegano_la_issue_e_la_issue_lo_mostra", func(t *testing.T) {
		n := e.openPR("bob", "feat", fmt.Sprintf(`,"body":"Aggiunge la cosa.\n\nFixes #%d e vedi anche #%d"`, i1, i5))
		if got := e.closing(n); !slices.Equal(got, []int64{i1}) {
			t.Fatalf("closingIssues = %v, volute [%d] (#%d è solo citata)", got, i1, i5)
		}
		if !e.willClose("app", i1, n) {
			t.Fatalf("la issue #%d non mostra «PR #%d will close this issue»: %v", i1, n, eventTypes(e.events("app", i1, "alice")))
		}
		if e.willClose("app", i5, n) {
			t.Fatalf("la issue #%d è solo citata, non va chiusa dalla PR", i5)
		}
		if s, _ := e.issueState("app", i1); s != "open" {
			t.Fatalf("la issue non si chiude prima del merge: %s", s)
		}
	})

	t.Run("merge_commit_chiude_come_completed", func(t *testing.T) {
		n := int64(e.count(`SELECT number FROM core.pull_requests WHERE source_branch = 'feat'`))
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if s, r := e.issueState("app", i1); s != "closed" || r != "completed" {
			t.Fatalf("dopo il merge commit la issue è %s/%s", s, r)
		}
		var found bool
		for _, ev := range e.events("app", i1, "alice") {
			if ev.Type == "closed" && ev.Actor != nil && ev.Actor.Username == "bob" && ev.Data != nil {
				pr, _ := (*ev.Data)["pullRequest"].(map[string]any)
				found = pr["number"] == float64(n) && (*ev.Data)["reason"] == "completed"
			}
		}
		if !found {
			t.Fatalf("manca l'evento «closed» di bob con la PR: %+v", e.events("app", i1, "alice"))
		}
		if s, _ := e.issueState("app", i5); s != "open" {
			t.Fatalf("la issue solo citata non si chiude")
		}
		// La chiusura passa dall'outbox come ogni chiusura (notifiche e webhook).
		if c := e.count(`SELECT count(*) FROM core.event_outbox WHERE name = 'issue.closed' AND payload->'issue'->>'number' = $1`, fmt.Sprint(i1)); c != 1 {
			t.Fatalf("eventi issue.closed per #%d = %d", i1, c)
		}
	})

	t.Run("squash_chiude_come_completed", func(t *testing.T) {
		n := e.openPR("bob", "fix", fmt.Sprintf(`,"body":"Closes #%d"`, i2))
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
		if s, r := e.issueState("app", i2); s != "closed" || r != "completed" {
			t.Fatalf("dopo lo squash la issue è %s/%s", s, r)
		}
	})

	t.Run("chiusa_senza_merge_non_chiude_niente", func(t *testing.T) {
		n := e.openPR("bob", "nope", fmt.Sprintf(`,"body":"resolves #%d"`, i3))
		if got := e.closing(n); !slices.Equal(got, []int64{i3}) {
			t.Fatalf("closingIssues = %v", got)
		}
		e.want(e.do(http.MethodPost, e.pull(n)+"/close", "bob", ""), http.StatusOK, "")
		if s, _ := e.issueState("app", i3); s != "open" {
			t.Fatalf("la PR chiusa senza merge ha chiuso la issue: %s", s)
		}
		// Riaperta e unita, invece, la chiude.
		e.want(e.do(http.MethodPost, e.pull(n)+"/reopen", "bob", ""), http.StatusOK, "")
		if s, _ := e.issueState("app", i3); s != "open" {
			t.Fatalf("la riapertura non chiude: %s", s)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
		if s, _ := e.issueState("app", i3); s != "closed" {
			t.Fatalf("la PR riaperta e unita non ha chiuso la issue: %s", s)
		}
	})

	t.Run("la_parola_tolta_dal_testo_scollega_la_issue", func(t *testing.T) {
		n := e.openPR("bob", "edit", fmt.Sprintf(`,"body":"fixes #%d"`, i4))
		if got := e.closing(n); !slices.Equal(got, []int64{i4}) {
			t.Fatalf("closingIssues = %v", got)
		}
		e.want(e.do(http.MethodPatch, e.pull(n), "bob", `{"body":"ripensata, nessuna issue"}`), http.StatusOK, "")
		if got := e.closing(n); len(got) != 0 {
			t.Fatalf("closingIssues dopo la modifica = %v", got)
		}
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if s, _ := e.issueState("app", i4); s != "open" {
			t.Fatalf("una issue scollegata si è chiusa: %s", s)
		}
	})

	t.Run("altro_repo_vuole_write_di_chi_scrive", func(t *testing.T) {
		// bob non ha write su alice/other: la citazione non collega niente.
		n := e.openPR("bob", "cross1", fmt.Sprintf(`,"body":"fixes alice/other#%d"`, o1))
		if c := e.count(`SELECT count(*) FROM core.pull_request_closing_issues c JOIN core.pull_requests p ON p.id = c.pull_request_id WHERE p.number = $1`, n); c != 0 {
			t.Fatalf("collegamenti senza write = %d", c)
		}
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if s, _ := e.issueState("other", o1); s != "open" {
			t.Fatalf("senza write sul repo della issue non si chiude: %s", s)
		}

		e.id.grantWrite(otherID, "bob")
		n = e.openPR("bob", "cross2", fmt.Sprintf(`,"body":"fixes alice/other#%d"`, o2))
		if got := e.closing(n); len(got) != 0 {
			t.Fatalf("closingIssues elenca solo le issue del repo della PR: %v", got)
		}
		e.want(e.merge("app", n, "bob", `{"method":"squash"}`), http.StatusOK, "")
		if s, r := e.issueState("other", o2); s != "closed" || r != "completed" {
			t.Fatalf("con write la issue di un altro repo si chiude: %s/%s", s, r)
		}
		if s, _ := e.issueState("other", o1); s != "open" {
			t.Fatalf("l'altra issue non va chiusa: %s", s)
		}
	})

	t.Run("una_issue_riaperta_dopo_il_collegamento_non_si_richiude", func(t *testing.T) {
		ir := e.newIssue("app", "riaperta")
		n := e.openPR("bob", "side", fmt.Sprintf(`,"body":"fixes #%d"`, ir))
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", ir), "alice", `{}`), http.StatusOK, "")
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/reopen", ir), "alice", ``), http.StatusOK, "")
		e.want(e.merge("app", n, "bob", `{"method":"merge"}`), http.StatusOK, "")
		if s, _ := e.issueState("app", ir); s != "open" {
			t.Fatalf("la issue riaperta dopo il collegamento è stata richiusa dal merge: %s", s)
		}
	})
}
