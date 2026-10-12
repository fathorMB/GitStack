//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

func TestAdminSystem(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	e.id.sysAdmin[carolID] = true // carol è l'amministratore dell'installazione

	file := filepath.Join(t.TempDir(), "system.json")
	doc := `{"schema":1,"database":{"mode":"external","host":"pg.acme.local","port":5432,"name":"gitstack","sslmode":"verify-full","serverVersion":"15.0-installer","password":"hunter2-segreto"},
	  "backup":{"last":{"state":"success","at":"2026-10-12T02:00:00Z"},"s3":{"configured":true,"bucket":"b","secret_key":"hunter2-segreto","upload":{"state":"failed","error":"timeout"}}},
	  "versions":{"gitstack":"v1.0.0","k3sUpdate":"patch"},"network":{"httpProxy":"http://u:hunter2-segreto@p.acme.local:3128"}}`
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	e.router = httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(e.git),
		httpserver.WithSystemInfo(httpserver.SystemInfoConfig{File: file}))

	get := func(user string) *httptest.ResponseRecorder { return e.do(http.MethodGet, "/admin/system", user, "") }

	t.Run("non_admin_403", func(t *testing.T) {
		for _, u := range []string{"alice", "bob"} {
			if rec := get(u); rec.Code != http.StatusForbidden {
				t.Errorf("%s: %d %s", u, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("admin_vede_stato_e_versione_viva", func(t *testing.T) {
		rec := get("carol")
		e.want(rec, http.StatusOK)
		var out openapi.AdminSystem
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Published || out.Database.Mode != "external" || *out.Database.Host != "pg.acme.local" {
			t.Errorf("database: %+v", out.Database)
		}
		// La versione viene da SHOW server_version, non dal file.
		if out.Database.ServerVersion == nil || *out.Database.ServerVersion == "15.0-installer" ||
			out.Database.VersionLive == nil || !*out.Database.VersionLive {
			t.Errorf("versione: %+v", out.Database)
		}
		if out.Backup.S3.Upload.State != "failed" || out.Versions.K3sUpdate != "patch" {
			t.Errorf("backup/versioni: %+v %+v", out.Backup, out.Versions)
		}
		if strings.Contains(rec.Body.String(), "hunter2-segreto") {
			t.Errorf("segreto nella risposta: %s", rec.Body.String())
		}
	})

	t.Run("senza_file_non_pubblicato", func(t *testing.T) {
		e.router = httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret,
			httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id),
			httpserver.WithSystemInfo(httpserver.SystemInfoConfig{File: filepath.Join(t.TempDir(), "assente.json")}))
		rec := get("carol")
		e.want(rec, http.StatusOK)
		var out openapi.AdminSystem
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Published || out.Database.Mode != "unknown" || out.Database.VersionLive == nil || !*out.Database.VersionLive {
			t.Errorf("%+v", out)
		}
	})
}
