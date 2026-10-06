//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-161: linkedCommitCount su IssueSummary, in lista e in ricerca.
func TestIssueSummary_LinkedCommitCount(t *testing.T) {
	e := newIssuesEnv(t)
	pub := e.repo("pub", true)
	secret := e.repo("secret", false) // solo alice
	nNone := e.open("pub", "alice", "senza commit")
	nMany := e.open("pub", "alice", "con commit")
	issueID := func(n int64) string {
		var id string
		if err := e.pool.QueryRow(t.Context(), `SELECT id::text FROM core.issues WHERE repo_id = $1 AND number = $2`, pub, n).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	_ = nNone
	id := issueID(nMany)
	ins := func(typ, sha, repoID string) {
		data, _ := json.Marshal(map[string]any{"commit": map[string]any{
			"sha": sha, "repositoryId": repoID, "repository": "x/y", "subject": "Fixes", "ref": "refs/heads/main"}})
		e.sql(`INSERT INTO core.issue_events (id, issue_id, type, data, created_at) VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp())`,
			id, typ, data)
	}
	sha := func(c string) string { return fmt.Sprintf("%040s", c) }
	ins("commit_linked", sha("a"), pub.String())
	ins("commit_linked", sha("b"), pub.String())
	ins("commit_linked", sha("c"), pub.String())
	ins("closed_by_commit", sha("c"), pub.String()) // stesso sha: conta una volta
	ins("commit_linked", sha("d"), secret.String()) // repo che carol non legge

	counts := func(user string) map[int64]int {
		out := map[int64]int{}
		rec := e.do(http.MethodGet, "/repos/alice/pub/issues?perPage=100", user, "")
		e.want(rec, http.StatusOK, "")
		var l openapi.IssueList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			out[it.Number] = it.LinkedCommitCount
		}
		rec = e.do(http.MethodGet, "/search/issues?perPage=100&q="+url.QueryEscape("repo:alice/pub"), user, "")
		e.want(rec, http.StatusOK, "")
		var s openapi.IssueSearchResultList
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		for _, it := range s.Items {
			if out[it.Issue.Number] != it.Issue.LinkedCommitCount {
				t.Fatalf("lista e ricerca divergono per #%d: %d vs %d", it.Issue.Number, out[it.Issue.Number], it.Issue.LinkedCommitCount)
			}
		}
		return out
	}
	alice := counts("alice")
	if alice[nNone] != 0 || alice[nMany] != 4 {
		t.Fatalf("alice: %v (attesi 0 e 4)", alice)
	}
	carol := counts("carol")
	if carol[nNone] != 0 || carol[nMany] != 3 {
		t.Fatalf("carol non legge secret: %v (attesi 0 e 3)", carol)
	}
}
