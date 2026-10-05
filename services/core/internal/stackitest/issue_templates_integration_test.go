//go:build integration

package stackitest

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIssueTemplates prova le letture dei modelli issue (I11) sullo stack
// vero: gateway e identity e git sono i binari, core è il router in-process
// con i client veri.
func TestIssueTemplates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non installato")
	}
	pool, dsn := dbtest.NewPool(t)
	_ = pool
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")
	gitBin := build(t, "../../../git", "git")

	identityAddr, gatewayAddr, gitAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	dataDir := t.TempDir()
	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret,
		httpserver.WithRepoIdentity(identityclient.New(mustURL(t, "http://"+identityAddr), serviceSecret, 5*time.Second)),
		httpserver.WithGit(gitclient.New(mustURL(t, "http://"+gitAddr), serviceSecret, 30*time.Second)),
	))
	t.Cleanup(coreSrv.Close)

	start(t, "identity", identityBin, identityAddr,
		"GITSTACK_IDENTITY_ADDR="+identityAddr,
		"GITSTACK_IDENTITY_DB_URL="+dsn,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_IDENTITY_ADMIN_USERNAME=admin",
		"GITSTACK_IDENTITY_ADMIN_PASSWORD="+adminPassword,
	)
	start(t, "git", gitBin, gitAddr,
		"GITSTACK_GIT_ADDR="+gitAddr,
		"GITSTACK_GIT_DATA_DIR="+dataDir,
		"GITSTACK_GIT_SSH_ADDR=off",
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
	)
	start(t, "gateway", gatewayBin, gatewayAddr,
		"GITSTACK_GATEWAY_ADDR="+gatewayAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	// --- utenti e token ------------------------------------------------------
	login := s.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, nil, nil)
	want(t, login, 200, "")
	var adminCookie *http.Cookie
	for _, c := range login.cookies {
		if c.Name == "gst_session" {
			adminCookie = c
		}
	}
	if adminCookie == nil {
		t.Fatalf("login admin: %s", login.body)
	}
	want(t, s.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, adminCookie), 204, "")

	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	mkUser := func(name string) {
		want(t, s.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, adminCookie), 201, "")
	}
	mkToken := func(name string, scopes ...string) map[string]string {
		l := s.gw("POST", "/auth/login", map[string]any{"username": name, "password": userPassword}, nil, nil)
		want(t, l, 200, "")
		var ck *http.Cookie
		for _, c := range l.cookies {
			if c.Name == "gst_session" {
				ck = c
			}
		}
		tok := s.gw("POST", "/user/tokens", map[string]any{"name": "t-" + strings.Join(scopes, "-"), "scopes": scopes, "expiresAt": exp}, nil, ck)
		want(t, tok, 201, "")
		token, _ := tok.json()["token"].(string)
		return map[string]string{"Authorization": "Bearer " + token}
	}
	mkUser("alice")
	alice := mkToken("alice", "read:resource", "write:resource")

	// --- repo vuoto: nessun .gitstack/ISSUE_TEMPLATE --------------------
	repoID := make(map[string]string)
	r := s.gw("POST", "/repos", map[string]any{
		"owner":      "alice",
		"name":       "tpl-empty",
		"visibility": "internal",
		"readme":     true,
	}, alice, nil)
	want(t, r, 201, "")
	repoID["empty"], _ = r.json()["id"].(string)

	t.Run("repo_vuoto", func(t *testing.T) {
		r := rawGet(t, s.gateway+"/v1/repos/alice/tpl-empty/issue-templates", alice)
		want(t, r, 200, "")
		var out struct {
			Items []struct{ Name string } `json:"items"`
		}
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatalf("JSON invalido: %s", string(r.body))
		}
		if out.Items == nil {
			t.Errorf("items = nil, voluto []")
		}
		if len(out.Items) != 0 {
			t.Errorf("items = %d, voluto 0", len(out.Items))
		}
	})

	// --- repo con modelli -----------------------------------------------
	r = s.gw("POST", "/repos", map[string]any{
		"owner":      "alice",
		"name":       "tpl-templates",
		"visibility": "internal",
		"readme":     true,
	}, alice, nil)
	want(t, r, 201, "")
	repoID["templates"], _ = r.json()["id"].(string)

	work := t.TempDir()
	runGit(t, work, nil, "init", "-q", "-b", scratch)
	write := func(p, c string) {
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# README\n")
	write(".gitstack/ISSUE_TEMPLATE/bug.md", "---\ntitle: Bug Report\nabout: Report a bug\nlabels:\n  - bug\n  - triage\n---\n\n## Steps to reproduce\n\n")
	write(".gitstack/ISSUE_TEMPLATE/feature.md", "---\ntitle: Feature Request\nabout: Suggest an idea\n---\n\n## Problem\n\n")
	write(".gitstack/ISSUE_TEMPLATE/broken.md", "---\ntitle: [INVALID\nbroken yaml {{{\n---\n\nBad\n")
	runGit(t, work, authorEnv("Alice", "alice@example.com"), "add", "-A")
	runGit(t, work, authorEnv("Alice", "alice@example.com"), "commit", "-q", "-m", "add issue templates")
	sha := runGit(t, work, nil, "rev-parse", "HEAD")

	bare := filepath.Join(dataDir, "repos", repoID["templates"][:2], repoID["templates"]+".git")
	runGit(t, bare, nil, "fetch", "-q", "--force", work, scratch+":refs/heads/main")

	t.Run("modelli_con_ordinamento", func(t *testing.T) {
		r := rawGet(t, s.gateway+"/v1/repos/alice/tpl-templates/issue-templates", alice)
		want(t, r, 200, "")
		var out struct {
			Items []struct {
				Name   string    `json:"name"`
				Title  *string   `json:"title"`
				About  *string   `json:"about"`
				Labels *[]string `json:"labels"`
				Body   string    `json:"body"`
			} `json:"items"`
		}
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatalf("JSON invalido: %s", string(r.body))
		}
		if len(out.Items) != 3 {
			t.Fatalf("3 modelli (broken saltato), trovato %d: %v", len(out.Items), itemNames(out.Items))
		}
		if out.Items[0].Name != "bug" || out.Items[1].Name != "feature" || out.Items[2].Name != "broken" {
			t.Errorf("nomi = %v, voluto [bug feature broken]", itemNames(out.Items))
		}
		// bug: con title, about, labels
		if out.Items[0].Title == nil || *out.Items[0].Title != "Bug Report" {
			t.Errorf("bug title = %v, voluto %q", out.Items[0].Title, "Bug Report")
		}
		if out.Items[0].About == nil || *out.Items[0].About != "Report a bug" {
			t.Errorf("bug about = %v", out.Items[0].About)
		}
		if out.Items[0].Labels == nil || len(*out.Items[0].Labels) != 2 {
			t.Errorf("bug labels = %v", out.Items[0].Labels)
		}
		// feature: senza labels
		if out.Items[1].Labels != nil {
			t.Errorf("feature labels = %v, voluto nil", *out.Items[1].Labels)
		}
		// broken: nessun front matter valido (salta tutto il file)
		if out.Items[2].Body == "" {
			t.Errorf("broken body vuoto")
		}
	})
}

// itemNames estrae i nomi dalle items.
func itemNames(items []struct{ Name string }) []string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}
