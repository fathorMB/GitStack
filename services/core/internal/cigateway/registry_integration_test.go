//go:build integration

package cigateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
)

func (e *env) registryAccess(token, owner, repo string) (int, cigateway.RegistryAccess) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+cigateway.RegistryAccessPath+"?owner="+owner+"&repo="+repo, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out cigateway.RegistryAccess
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			e.t.Fatal(err)
		}
	}
	return res.StatusCode, out
}

// TestRegistryAccess_NomeConMaiuscole: R11, il repo `Uno` si chiede come `uno` (docker accetta solo
// minuscole) e la risposta ha il nome com'è in core.
func TestRegistryAccess_NomeConMaiuscole(t *testing.T) {
	e := newEnv(t)
	repo, _ := e.queue("Uno", "a")
	ta, err := e.fetch(runnerSecret)
	if err != nil || ta == nil {
		t.Fatal(ta, err)
	}
	tok := taskToken(t, ta)
	for _, name := range []string{"uno", "Uno", "UNO"} {
		st, got := e.registryAccess(tok, "alice", name)
		if st != 200 || got.RepoID != repo.String() || got.Name != "Uno" || got.Owner != "alice" {
			t.Errorf("%s: %d %+v, atteso 200 e Name come in core (Uno)", name, st, got)
		}
	}
}

// TestRegistryAccess_SoloIlRepoDelJob: il token del job dice cosa può fare sul registry del
// suo repo (livello packages, archiviazione) e per ogni altro repo, o senza token, non dà nulla.
func TestRegistryAccess_SoloIlRepoDelJob(t *testing.T) {
	e := newEnv(t)
	repoA, _ := e.queue("uno", "a")
	e.queue("due", "b")
	ta, err := e.fetch(runnerSecret)
	if err != nil || ta == nil {
		t.Fatal(ta, err)
	}
	tok := taskToken(t, ta)

	st, got := e.registryAccess(tok, "alice", "uno")
	if st != 200 || got.RepoID != repoA.String() || got.Owner != "alice" || got.Name != "uno" || got.Workflow != "due" || got.Job != "build" || got.Archived {
		t.Fatalf("repo del job: %d %+v", st, got)
	}
	if got.Packages != "read" {
		t.Errorf("senza permissions: nel workflow il default è read, trovato %q", got.Packages)
	}
	if st, _ := e.registryAccess(tok, "ALICE", "uno"); st != 200 {
		t.Errorf("owner con altre maiuscole: %d", st)
	}
	if st, got := e.registryAccess(tok, "alice", "UNO"); st != 200 || got.Name != "uno" {
		t.Errorf("nome con altre maiuscole: %d %+v", st, got)
	}
	if st, _ := e.registryAccess(tok, "alice", "due"); st != 404 {
		t.Errorf("altro repo: %d", st)
	}
	if st, _ := e.registryAccess("", "alice", "uno"); st != 401 {
		t.Errorf("senza token: %d", st)
	}
	if st, _ := e.registryAccess("gsj_sconosciuto", "alice", "uno"); st != 401 {
		t.Errorf("token sconosciuto: %d", st)
	}

	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE core.ci_jobs SET permissions = '{"contents":"read","packages":"write","actions":"read"}'::jsonb WHERE id = (SELECT id FROM core.ci_jobs WHERE task_id = $1)`, ta.GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoA); err != nil {
		t.Fatal(err)
	}
	if st, got := e.registryAccess(tok, "alice", "uno"); st != 200 || got.Packages != "write" || !got.Archived {
		t.Errorf("write + archiviato: %d %+v", st, got)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE core.repositories SET deleted_at = now() WHERE resource_id = $1`, repoA); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.registryAccess(tok, "alice", "uno"); st != 404 {
		t.Errorf("repo eliminato: %d", st)
	}
}
