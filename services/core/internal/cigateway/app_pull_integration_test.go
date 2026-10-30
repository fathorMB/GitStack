//go:build integration

package cigateway_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/apppull"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
)

// TestRegistryAccess_TokenDiPullDellApp (GIT-425): il token di pull di un'app dà sola lettura sul
// registry del repo di quell'app e su nessun altro, sparisce con l'app o con il repo e non vale
// sul gateway esposto fuori dal cluster.
func TestRegistryAccess_TokenDiPullDellApp(t *testing.T) {
	e := newEnv(t)
	repoA, _ := e.queue("uno", "a")
	e.queue("due", "b")
	keys := []string{"chiave-gateway"}
	appID := uuid.New()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO core.apps (id, repo_id, name, guaranteed_cpu_millis, guaranteed_memory_bytes,
		limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES ($1, $2, 'web', 100, 1000000, 200, 2000000, now(), now())`, appID, repoA); err != nil {
		t.Fatal(err)
	}
	tok, err := apppull.Mint(keys, appID, e.now())
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(ext bool) cigateway.Config {
		return cigateway.Config{Pool: e.pool, Source: e.src, Reader: e.rd, Logs: e.logs, Now: e.now, ExternalOnly: ext,
			AppPullKeys: keys, PollTimeout: 400 * time.Millisecond, PollEvery: 25 * time.Millisecond}
	}
	srv := httptest.NewServer(cigateway.New(cfg(false)))
	t.Cleanup(srv.Close)
	ext := httptest.NewServer(cigateway.New(cfg(true)))
	t.Cleanup(ext.Close)
	access := func(base, token, owner, repo string) (int, cigateway.RegistryAccess) {
		e.t.Helper()
		req, _ := http.NewRequest("GET", base+cigateway.RegistryAccessPath+"?owner="+owner+"&repo="+repo, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			e.t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var out cigateway.RegistryAccess
		if res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				e.t.Fatal(err)
			}
		}
		return res.StatusCode, out
	}

	st, got := access(srv.URL, tok, "alice", "uno")
	if st != http.StatusOK || got.RepoID != repoA.String() || got.Packages != "read" || got.Archived {
		t.Fatalf("repo dell'app: %d %+v", st, got)
	}
	if st, _ := access(srv.URL, tok, "alice", "due"); st != http.StatusNotFound {
		t.Errorf("altro repo: %d", st)
	}
	if st, _ := access(ext.URL, tok, "alice", "uno"); st != http.StatusUnauthorized {
		t.Errorf("gateway esposto: %d", st)
	}
	other, _ := apppull.Mint([]string{"altra-chiave"}, appID, e.now())
	if st, _ := access(srv.URL, other, "alice", "uno"); st != http.StatusUnauthorized {
		t.Errorf("firma di un'altra chiave: %d", st)
	}
	old, _ := apppull.Mint(keys, appID, e.now().Add(-apppull.TTL-time.Hour))
	if st, _ := access(srv.URL, old, "alice", "uno"); st != http.StatusUnauthorized {
		t.Errorf("token scaduto: %d", st)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoA); err != nil {
		t.Fatal(err)
	}
	if st, got := access(srv.URL, tok, "alice", "uno"); st != http.StatusOK || !got.Archived || got.Packages != "read" {
		t.Errorf("repo archiviato si scarica ancora: %d %+v", st, got)
	}
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM core.apps WHERE id = $1`, appID); err != nil {
		t.Fatal(err)
	}
	if st, _ := access(srv.URL, tok, "alice", "uno"); st != http.StatusNotFound {
		t.Errorf("app eliminata: %d", st)
	}
}
