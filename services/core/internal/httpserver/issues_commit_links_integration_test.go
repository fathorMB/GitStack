//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// C1, C2 (GIT-131): il collegamento e la chiusura da commit di un altro repo
// compaiono nella cronologia solo a chi legge anche quel repo; il repositoryId
// interno non esce dall'API.
func TestIssueEvents_CommitDiAltroRepoVisibileSoloAChiVedeEntrambi(t *testing.T) {
	e := newIssuesEnv(t)
	pub := e.repo("pub", true)        // leggibile da tutti
	secret := e.repo("secret", false) // solo alice
	n := e.open("pub", "alice", "da chiudere")
	var issueID string
	if err := e.pool.QueryRow(t.Context(), `SELECT id::text FROM core.issues WHERE repo_id = $1 AND number = $2`, pub, n).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	ins := func(typ, repoID, repoName string) {
		data, _ := json.Marshal(map[string]any{"commit": map[string]any{
			"sha": "1111111111111111111111111111111111111111", "repository": repoName, "repositoryId": repoID,
			"subject": "Fixes #1", "ref": "refs/heads/main", "authorName": "Ada"}})
		e.sql(`INSERT INTO core.issue_events (id, issue_id, type, data, created_at) VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp())`,
			issueID, typ, data)
	}
	ins("commit_linked", pub.String(), "alice/pub")
	ins("commit_linked", secret.String(), "alice/secret")
	ins("closed_by_commit", secret.String(), "alice/secret")

	alice := eventTypes(e.events("pub", n, "alice"))
	if !slices.Equal(alice, []string{"opened", "commit_linked", "commit_linked", "closed_by_commit"}) {
		t.Fatalf("alice vede entrambi i repo: %v", alice)
	}
	carolEvs := e.events("pub", n, "carol")
	if got := eventTypes(carolEvs); !slices.Equal(got, []string{"opened", "commit_linked"}) {
		t.Fatalf("carol non vede secret: %v", got)
	}
	for _, ev := range e.events("pub", n, "alice") {
		if c, ok := (*ev.Data)["commit"].(map[string]any); ok {
			if _, leaked := c["repositoryId"]; leaked {
				t.Fatalf("repositoryId esposto: %v", c)
			}
		}
	}
	// Il totale e la paginazione contano solo gli eventi visibili.
	rec := e.do(http.MethodGet, fmt.Sprintf("/repos/alice/pub/issues/%d/events?perPage=1&page=2", n), "carol", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.IssueEventList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	if l.Total != 2 || len(l.Items) != 1 || l.Items[0].Type != "commit_linked" {
		t.Fatalf("pagina 2 di carol: total=%d items=%v", l.Total, eventTypes(l.Items))
	}
}
