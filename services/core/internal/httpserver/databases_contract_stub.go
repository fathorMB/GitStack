package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Database gestiti (v3-d, ADR 0003). Elenco, creazione, lettura, ridimensionamento, eliminazione,
// rotazione, log e metriche sono in databases.go (GIT-551). Le altre operazioni arrivano con
// GIT-552 (collegamenti), GIT-553 (backup), GIT-554 (LAN) e GIT-557 (impostazioni d'istanza): finche'
// non sono implementate rispondono 422 `databases_disabled`, l'errore stabile del contratto — mai
// 501 (difetto di GIT-547).
func databaseDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "databases_disabled",
		"I database gestiti sono disattivati in questa installazione: li attiva l'amministratore (gitstack config set databases).")
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

func (s *apiServer) GetAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	databaseDisabled(w)
}

func (s *apiServer) UpdateAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	databaseDisabled(w)
}
