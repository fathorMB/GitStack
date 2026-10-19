//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-266 (PR12a): visibilità, rapporto con la ricerca delle issues,
// richieste non valide e paginazione. Dati di seedPulls.

func TestPullSearch_PR12a_VisibilitaEIssues(t *testing.T) {
	e := seedPulls(t)
	open := []string{"alice/app#1", "alice/app#2", "alice/app#3", "alice/app#4", "alice/app#5", "alice/app#6"}

	t.Run("solo_i_repo_visibili", func(t *testing.T) {
		// carol legge solo app: le PR di secret non compaiono in nessun filtro
		for _, q := range []string{"", "is:pr", "is:draft", "author:@agents", "review:required", "-is:merged"} {
			for _, k := range e.globalPulls("carol", q) {
				if strings.HasPrefix(k, "alice/secret") {
					t.Errorf("q=%q: carol vede %s", q, k)
				}
			}
		}
		if got := e.globalPulls("carol", "author:@agents"); !slices.Equal(got, []string{"alice/app#3"}) {
			t.Errorf("author:@agents per carol = %v", got)
		}
		if got := e.globalPulls("carol", "is:draft"); !slices.Equal(got, []string{"alice/app#2"}) {
			t.Errorf("is:draft per carol = %v", got)
		}
		if got := e.globalPulls("carol", "repo:alice/secret"); len(got) != 0 {
			t.Errorf("repo:alice/secret per carol = %v", got)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/secret/pulls", "carol", ""), http.StatusNotFound, "not_found")
		// chi ha accesso le vede: alice (admin) e bob (write)
		for _, u := range []string{"alice", "bob"} {
			if got := e.globalPulls(u, "is:draft"); !slices.Equal(got, []string{"alice/app#2", "alice/secret#2"}) {
				t.Errorf("is:draft per %s = %v", u, got)
			}
			if got := e.globalPulls(u, "author:@agents"); !slices.Equal(got, []string{"alice/app#3", "alice/secret#1"}) {
				t.Errorf("author:@agents per %s = %v", u, got)
			}
		}
		want := append(slices.Clone(open), "alice/secret#1", "alice/secret#2")
		sort.Strings(want)
		if got := e.globalPulls("alice", ""); !slices.Equal(got, want) {
			t.Errorf("tutte le aperte per alice = %v, volute %v", got, want)
		}
		if got := e.globalPulls("alice", "org:acme"); len(got) != 0 {
			t.Errorf("org:acme = %v", got)
		}
		if got := e.globalPulls("alice", "repo:alice/secret review:required"); len(got) != 0 {
			t.Errorf("review:required in secret = %v", got)
		}
	})

	t.Run("le_issues_non_sono_PR", func(t *testing.T) {
		// la ricerca delle issues non restituisce mai una PR né applica review:
		for _, q := range []string{"is:pr", "is:draft", "is:merged", "review:required", "review:approved"} {
			rec := e.do(http.MethodGet, "/search/issues?q="+url.QueryEscape(q), "alice", "")
			e.want(rec, http.StatusOK, "")
			var l openapi.IssueSearchResultList
			if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || l.Total != 0 {
				t.Errorf("/search/issues q=%q: %v totale %d", q, err, l.Total)
			}
			rec = e.do(http.MethodGet, "/repos/alice/app/issues?state=all&q="+url.QueryEscape(q), "alice", "")
			e.want(rec, http.StatusOK, "")
			var il openapi.IssueList
			if err := json.Unmarshal(rec.Body.Bytes(), &il); err != nil || il.Total != 0 {
				t.Errorf("listIssues q=%q: %v totale %d", q, err, il.Total)
			}
		}
		// -is:pr le trova: la sola issue di app (numero 9)
		rec := e.do(http.MethodGet, "/search/issues?q="+url.QueryEscape("-is:pr -review:approved"), "alice", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.IssueSearchResultList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || l.Total != 1 || l.Items[0].Issue.Number != 9 {
			t.Fatalf("-is:pr = %v %+v", err, l)
		}
		if got := e.globalPulls("alice", "una issue"); len(got) != 0 {
			t.Errorf("la issue compare fra le PR: %v", got)
		}
	})

	t.Run("richieste_non_valide_e_paginazione", func(t *testing.T) {
		for _, q := range []string{"review:done", "is:nope", "assignee:", "foo:bar"} {
			e.want(e.do(http.MethodGet, "/search/pulls?q="+url.QueryEscape(q), "alice", ""), http.StatusUnprocessableEntity, "validation_failed")
			e.want(e.do(http.MethodGet, "/repos/alice/app/pulls?q="+url.QueryEscape(q), "alice", ""), http.StatusUnprocessableEntity, "validation_failed")
		}
		e.want(e.do(http.MethodGet, "/search/pulls?page=0", "alice", ""), http.StatusBadRequest, "invalid_page")
		// con review: si pagina dopo il filtro, con il totale giusto
		rec := e.do(http.MethodGet, "/search/pulls?perPage=1&page=2&q="+url.QueryEscape("review:required"), "alice", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.PullRequestSearchResultList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || l.Total != 2 || len(l.Items) != 1 || l.Items[0].PullRequest.Number != 1 {
			t.Fatalf("seconda pagina = %v %+v", err, l)
		}
		if l.Items[0].PullRequest.ReviewDecision != "required" {
			t.Fatalf("decisione = %s", l.Items[0].PullRequest.ReviewDecision)
		}
		// lo stato esplicito di listPullRequests si somma a q
		rec = e.do(http.MethodGet, "/repos/alice/app/pulls?state=closed&q="+url.QueryEscape("author:bob"), "alice", "")
		e.want(rec, http.StatusOK, "")
		var pl openapi.PullRequestList
		if err := json.Unmarshal(rec.Body.Bytes(), &pl); err != nil || pl.Total != 1 || pl.Items[0].Number != 7 {
			t.Fatalf("state=closed = %v %+v", err, pl)
		}
	})
}
