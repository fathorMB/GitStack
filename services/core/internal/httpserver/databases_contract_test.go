package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-550 (v3-d): la funzione database gestiti e' spenta di default
// (interruttore d'istanza, DB23) e l'implementazione arriva con
// GIT-551…GIT-557: ogni operazione del tag databases risponde 422
// `databases_disabled`, l'errore stabile del contratto — mai 501 (difetto di
// GIT-547). Lo stub non tocca lo store, quindi basta un apiServer a valori
// zero e si chiama l'handler direttamente.

func TestDatabases_FunzioneSpentaErroreStabile(t *testing.T) {
	s := &apiServer{}
	req := httptest.NewRequest(http.MethodGet, "/v1/databases/alice/shop", nil)
	cases := []struct {
		name string
		call func(w http.ResponseWriter)
	}{
		{"list", func(w http.ResponseWriter) { s.ListDatabases(w, req, "alice") }},
		{"create", func(w http.ResponseWriter) { s.CreateDatabase(w, req, "alice") }},
		{"get", func(w http.ResponseWriter) { s.GetDatabase(w, req, "alice", "shop") }},
		{"update", func(w http.ResponseWriter) { s.UpdateDatabase(w, req, "alice", "shop") }},
		{"delete", func(w http.ResponseWriter) { s.DeleteDatabase(w, req, "alice", "shop", openapi.DeleteDatabaseParams{}) }},
		{"rotate", func(w http.ResponseWriter) { s.RotateDatabaseCredentials(w, req, "alice", "shop") }},
		{"link_list", func(w http.ResponseWriter) { s.ListDatabaseLinks(w, req, "alice", "shop") }},
		{"link_create", func(w http.ResponseWriter) { s.CreateDatabaseLink(w, req, "alice", "shop") }},
		{"link_delete", func(w http.ResponseWriter) { s.DeleteDatabaseLink(w, req, "alice", "shop", linkID) }},
		{"link_rotate", func(w http.ResponseWriter) { s.RotateDatabaseLink(w, req, "alice", "shop", linkID) }}, {"backup_list", func(w http.ResponseWriter) { s.ListDatabaseBackups(w, req, "alice", "shop") }},
		{"backup_create", func(w http.ResponseWriter) { s.CreateDatabaseBackup(w, req, "alice", "shop") }},
		{"backup_restore", func(w http.ResponseWriter) { s.RestoreDatabaseBackup(w, req, "alice", "shop", backupID) }},
		{"lan_get", func(w http.ResponseWriter) { s.GetDatabaseLan(w, req, "alice", "shop") }},
		{"lan_set", func(w http.ResponseWriter) { s.SetDatabaseLan(w, req, "alice", "shop") }},
		{"logs", func(w http.ResponseWriter) {
			s.GetDatabaseLogs(w, req, "alice", "shop", openapi.GetDatabaseLogsParams{})
		}},
		{"metrics", func(w http.ResponseWriter) {
			s.GetDatabaseMetrics(w, req, "alice", "shop", openapi.GetDatabaseMetricsParams{})
		}},
		{"admin_settings_get", func(w http.ResponseWriter) { s.GetAdminDatabasesSettings(w, req) }},
		{"admin_settings_update", func(w http.ResponseWriter) { s.UpdateAdminDatabasesSettings(w, req) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, atteso 422 (mai 501)", rec.Code)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("corpo non JSON: %v", err)
			}
			if body.Error.Code != "databases_disabled" {
				t.Errorf("code %q, atteso databases_disabled", body.Error.Code)
			}
			if !strings.Contains(body.Error.Message, "gitstack config set databases") {
				t.Errorf("messaggio %q non dice come accendere la funzione", body.Error.Message)
			}
		})
	}
}

var (
	linkID   openapi.DatabaseLinkIdParam
	backupID openapi.DatabaseBackupIdParam
)
