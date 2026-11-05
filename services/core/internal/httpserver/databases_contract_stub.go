package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Database gestiti (v3-d, GIT-550): il contratto e le dichiarazioni di
// sicurezza sono su main, l'implementazione arriva con GIT-551…GIT-557. La
// funzione e' spenta di default (interruttore d'istanza, DB23): finche' non
// e' implementata ogni operazione risponde 422 `databases_disabled`, l'errore
// stabile del contratto — mai 501 (difetto di GIT-547). Il controllo dei
// permessi (admin sull'owner, 404 a chi non lo e', DB2) si applica con la
// funzione accesa, quando lo store esiste (GIT-551).
func databaseDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "databases_disabled",
		"I database gestiti sono disattivati in questa installazione: li attiva l'amministratore (gitstack config set databases).")
}

func (s *apiServer) ListDatabases(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	databaseDisabled(w)
}

func (s *apiServer) CreateDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	databaseDisabled(w)
}

func (s *apiServer) GetDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) UpdateDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) DeleteDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.DeleteDatabaseParams) {
	databaseDisabled(w)
}

func (s *apiServer) RotateDatabaseCredentials(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) ListDatabaseLinks(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) CreateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) DeleteDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi.DatabaseLinkIdParam) {
	databaseDisabled(w)
}

func (s *apiServer) RotateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi.DatabaseLinkIdParam) {
	databaseDisabled(w)
}

func (s *apiServer) ListDatabaseBackups(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) CreateDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) RestoreDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, backupId openapi.DatabaseBackupIdParam) {
	databaseDisabled(w)
}

func (s *apiServer) GetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) SetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	databaseDisabled(w)
}

func (s *apiServer) GetDatabaseLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseLogsParams) {
	databaseDisabled(w)
}

func (s *apiServer) GetDatabaseMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseMetricsParams) {
	databaseDisabled(w)
}

func (s *apiServer) GetAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	databaseDisabled(w)
}

func (s *apiServer) UpdateAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	databaseDisabled(w)
}
