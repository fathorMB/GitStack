//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// MP2/MP7 (GIT-195): un repo con un mirror in pull attivo (anche in pausa) mostra
// `mirror: {origin, lastSyncedAt}` nella lettura e nell'elenco; un repo normale,
// o con il mirror interrotto, no. Le operazioni sono provate in mirrors_pull_engine_integration_test.go.
func TestPullMirror_CampoMirrorNelRepo(t *testing.T) {
	e := newMirrorEnv(t)
	repoID := e.repo("app", true)
	e.repo("normale", true)
	ctx := context.Background()

	synced := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, state, last_success_at, created_by)
		VALUES ($1, $2, 'https://github.com/o/r.git', 'paused', $3, $4)`, uuid.New(), repoID, synced, uuid.New()); err != nil {
		t.Fatal(err)
	}

	getRepo := func(name string) openapi.Repository {
		t.Helper()
		rec := e.do(http.MethodGet, "/repos/alice/"+name, "alice", "")
		e.want(rec, http.StatusOK, "")
		var r openapi.Repository
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	app := getRepo("app")
	if app.Mirror == nil || app.Mirror.Origin != "https://github.com/o/r.git" || app.Mirror.LastSyncedAt == nil || !app.Mirror.LastSyncedAt.Equal(synced) {
		t.Fatalf("mirror del repo: %+v", app.Mirror)
	}
	if getRepo("normale").Mirror != nil {
		t.Fatal("un repo normale non ha il campo mirror")
	}

	rec := e.do(http.MethodGet, "/repos?owner=alice", "alice", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.RepositoryList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	withMirror := 0
	for _, it := range l.Items {
		if it.Mirror != nil {
			withMirror++
			if it.Name != "app" {
				t.Fatalf("mirror sul repo sbagliato: %s", it.Name)
			}
		}
	}
	if withMirror != 1 {
		t.Fatalf("repo con mirror nell'elenco: %d", withMirror)
	}

	// Interrotto: il repo è normale.
	if _, err := e.pool.Exec(ctx, `UPDATE core.repo_pull_mirrors SET state = 'stopped', stopped_at = now(), next_attempt_at = NULL WHERE repo_id = $1`, repoID); err != nil {
		t.Fatal(err)
	}
	if getRepo("app").Mirror != nil {
		t.Fatal("un mirror interrotto non compare nel repo")
	}
}
