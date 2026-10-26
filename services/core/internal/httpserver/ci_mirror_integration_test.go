//go:build integration

package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// GIT-374 (CI3): l'interruttore «Esegui i workflow sul mirror» lo cambia solo chi ha admin sul repo.
func TestCIMirror_InterruttoreSoloAdmin(t *testing.T) {
	e := newHookEnv(t)
	repoID := e.repo("app", true)
	e.repo("normale", true)
	e.hid.grantWrite(repoID, "bob")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, state, created_by)
		VALUES ($1, $2, 'https://github.com/o/r.git', 'in_sync', $3)`, uuid.New(), repoID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	const path = "/repos/alice/app/actions/pull-mirror"

	// Senza admin: 403 in lettura e in scrittura, e lo stato non cambia.
	e.want(e.do(http.MethodGet, path, "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, path, "bob", `{"runOnPullMirror":true}`), http.StatusForbidden, "forbidden")

	// Spenta di default.
	rec := e.do(http.MethodGet, path, "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"runOnPullMirror":false`) || !strings.Contains(rec.Body.String(), `"pullMirror":true`) {
		t.Fatalf("default: %s", rec.Body.String())
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.ci_repo_state WHERE repo_id = $1 AND run_on_pull_mirror`, repoID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("il 403 ha acceso qualcosa: %d %v", n, err)
	}

	// Con admin si accende (e registra chi) e si spegne.
	rec = e.do(http.MethodPut, path, "alice", `{"runOnPullMirror":true}`)
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"runOnPullMirror":true`) || !strings.Contains(rec.Body.String(), `"enabledBy"`) {
		t.Fatalf("dopo l'accensione: %s", rec.Body.String())
	}
	var by *uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT run_on_pull_mirror_by FROM core.ci_repo_state WHERE repo_id = $1`, repoID).Scan(&by); err != nil || by == nil {
		t.Fatalf("chi ha acceso la CI non è registrato: %v %v", by, err)
	}
	rec = e.do(http.MethodPut, path, "alice", `{"runOnPullMirror":false}`)
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"runOnPullMirror":false`) {
		t.Fatalf("dopo lo spegnimento: %s", rec.Body.String())
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT run_on_pull_mirror_by FROM core.ci_repo_state WHERE repo_id = $1`, repoID).Scan(&by); err != nil || by != nil {
		t.Fatalf("spenta ma con autore: %v %v", by, err)
	}

	// Un repo che non è un mirror in pull non si può accendere; JSON sbagliato 422.
	e.want(e.do(http.MethodPut, "/repos/alice/normale/actions/pull-mirror", "alice", `{"runOnPullMirror":true}`), http.StatusConflict, "not_a_pull_mirror")
	e.want(e.do(http.MethodPut, path, "alice", `{}`), http.StatusUnprocessableEntity, "")
}
