//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// GIT-266 (v1.1): ricerca I10 estesa alle PR (PR12a) e casella Viewed
// personale (PR12b). Nel repo «app» (internal, leggibile da tutti) bob scrive
// le PR, bot è un agente; «secret» è privato (solo alice e bob).

// prKeys elenca «repo#n» ordinati di una risposta di ricerca globale.
func (e *reviewEnv) globalPulls(user, q string) []string {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/search/pulls?perPage=100&q="+url.QueryEscape(q), user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestSearchResultList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	out := []string{}
	for _, it := range l.Items {
		out = append(out, fmt.Sprintf("%s#%d", it.Repo, it.PullRequest.Number))
	}
	sort.Strings(out)
	return out
}

// repoPulls cerca nel repo alice/app: numeri ordinati.
func (e *reviewEnv) repoPulls(user, q string) []int64 {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/app/pulls?perPage=100&q="+url.QueryEscape(q), user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	out := []int64{}
	for _, it := range l.Items {
		out = append(out, it.Number)
	}
	slices.Sort(out)
	return out
}

// seedPulls crea le PR 1-8 di app e le due di secret (vedi i commenti).
func seedPulls(t *testing.T) *reviewEnv {
	t.Helper()
	e := newReviewEnv(t)
	// PR 1: bob, revisore richiesto bot; 2: bozza di bob; 3: dell'agente bot;
	// 4: approvata da alice; 5: alice chiede modifiche; 6: revisore richiesto
	// carol; 7: chiusa; 8: unita. Il numero #n è condiviso con le issues.
	p := map[int]int64{}
	for i := 1; i <= 8; i++ {
		author := "bob"
		if i == 3 {
			author = "bot"
		}
		p[i] = e.newPR(fmt.Sprintf("feat%d", i), author)
		if p[i] != int64(i) {
			t.Fatalf("PR %d ha numero %d", i, p[i])
		}
	}
	reqRev := func(n int64, user, who string) {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/requested-reviewers", n), user, fmt.Sprintf(`{"reviewers":[%q]}`, who)), http.StatusOK, "")
	}
	e.want(e.do(http.MethodPost, "/repos/alice/app/pulls/2/draft", "bob", ""), http.StatusOK, "")
	reqRev(1, "alice", "bot")
	reqRev(6, "bob", "carol")
	e.want(e.review(4, "alice", `{"event":"approve"}`), http.StatusCreated, "")
	e.want(e.review(5, "alice", `{"event":"request_changes","body":"no"}`), http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/alice/app/pulls/7/close", "alice", ""), http.StatusOK, "")
	e.sql(`UPDATE core.pull_requests SET state = 'merged', merge_method = 'merge', merged_by = $1, merged_at = now(), merge_commit_sha = $2
		WHERE repo_id = $3 AND number = 8`, uuid.MustParse(aliceID), sha(9), e.repo)

	// Un repo privato che carol non legge, con una PR di bot e una di alice.
	secret := e.pullsEnv.repo("secret", false)
	e.id.grantWrite(secret, "bob")
	e.id.mu.Lock()
	e.id.grants[secret] = append(e.id.grants[secret], bobID) // il finto identity elenca i repo leggibili dai grant
	e.id.mu.Unlock()
	e.id.grantWrite(secret, "bot")
	e.branch(secret, "s1")
	e.branch(secret, "s2")
	e.pullsEnv.open("secret", "bot", "s1")
	sn := e.pullsEnv.open("secret", "alice", "s2")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/secret/pulls/%d/draft", sn), "alice", ""), http.StatusOK, "")

	// Una issue di app (numero 9) per provare che is:pr non la include.
	e.pullsEnv.issuesEnv.open("app", "alice", "una issue")

	return e
}

func TestPullSearch_PR12a_Filtri(t *testing.T) {
	e := seedPulls(t)
	type tc struct {
		q    string
		want []int64
	}
	for _, c := range []tc{
		{"", []int64{1, 2, 3, 4, 5, 6}}, // senza stato valgono le aperte
		{"is:pr", []int64{1, 2, 3, 4, 5, 6}},
		{"is:draft", []int64{2}},
		{"-is:draft", []int64{1, 3, 4, 5, 6}},
		{"is:closed", []int64{7}},
		{"is:merged", []int64{8}},
		{"is:open is:merged", nil},
		{"is:issue", nil},
		{"review:approved", []int64{4}},
		{"review:changes-requested", []int64{5}},
		{"review:required", []int64{1, 6}},
		{"-review:required is:open", []int64{2, 3, 4, 5}},
		{"review:approved review:changes-requested", nil},
		{"author:@agents", []int64{3}},
		{"-author:@agents", []int64{1, 2, 4, 5, 6}},
		{"author:bob", []int64{1, 2, 4, 5, 6}},
		{"assignee:@agents", []int64{1}},
		{"assignee:carol", []int64{6}},
		{"assignee:@me", []int64{6}}, // carol è la revisora richiesta della 6
		{"no:assignee", []int64{2, 3, 4, 5}},
		{"label:bug", nil},
		{"milestone:v1", nil},
		{"reason:completed", nil},
		{"-label:bug is:closed", []int64{7}},
		{"no:label no:milestone is:merged", []int64{8}},
		// combinazioni con i filtri di I10 e con testo libero
		{"review:required author:bob", []int64{1, 6}},
		{"review:required assignee:@agents", []int64{1}},
		{"review:approved author:@agents", nil},
		{"is:draft author:bob no:assignee", []int64{2}},
		{"is:draft review:approved", nil},
		{"review:changes-requested feat5", []int64{5}},
		{"review:changes-requested feat4", nil},
		{`"PR feat3" author:@agents`, []int64{3}},
		{"-feat1 review:required", []int64{6}},
		{"is:closed author:bob", []int64{7}},
	} {
		got := e.repoPulls("carol", c.q)
		if !slices.Equal(got, nonNil(c.want)) {
			t.Errorf("repo q=%q: %v, voluto %v", c.q, got, c.want)
		}
		// stessa ricerca su tutta l'installazione ristretta al repo
		var wantG []string
		for _, n := range c.want {
			wantG = append(wantG, fmt.Sprintf("alice/app#%d", n))
		}
		if got := e.globalPulls("carol", "repo:alice/app "+c.q); !slices.Equal(got, nonNilS(wantG)) {
			t.Errorf("globale q=%q: %v, voluto %v", c.q, got, wantG)
		}
	}
}

func nonNil(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

func nonNilS(v []string) []string {
	if v == nil {
		return []string{}
	}
	sort.Strings(v)
	return v
}
