//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// MP2 (GIT-198): sul repo mirror in pull solo il codice è in sola lettura. Le
// issues, i commenti, le etichette e le milestone restano normali, anche con
// il mirror in pausa.
func TestPullMirror_IssuesEdEtichetteRestanoNormali(t *testing.T) {
	e := newMirrorEnv(t)
	repoID := e.repo("app", true)
	ctx := context.Background()

	for _, state := range []string{"in_sync", "paused"} {
		if _, err := e.pool.Exec(ctx, `DELETE FROM core.repo_pull_mirrors WHERE repo_id = $1`, repoID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.pool.Exec(ctx, `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, state, created_by)
			VALUES ($1, $2, 'https://github.com/o/r.git', $3, $4)`, uuid.New(), repoID, state, uuid.New()); err != nil {
			t.Fatal(err)
		}

		e.want(e.do(http.MethodPost, "/repos/alice/app/labels", "alice",
			fmt.Sprintf(`{"name":"bug-%s","color":"ff0000"}`, state)), http.StatusCreated, "")
		e.want(e.do(http.MethodPost, "/repos/alice/app/milestones", "alice",
			fmt.Sprintf(`{"title":"v-%s"}`, state)), http.StatusCreated, "")

		n := e.open("app", "alice", "da mirror "+state)
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/comments", n), "alice",
			`{"body":"un commento"}`), http.StatusCreated, "")
		e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/labels", n), "alice",
			fmt.Sprintf(`{"labels":["bug-%s"]}`, state)), http.StatusOK, "")
	}
}
