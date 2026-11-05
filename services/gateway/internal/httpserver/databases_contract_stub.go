package httpserver

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Le operazioni dei database gestiti (v3-d, tag databases) sono servite da
// core; con la funzione spenta (default, DB23) core risponde 422
// `databases_disabled`, mai 501. Il gateway le inoltra comunque per
// applicare la sicurezza generata dal contratto.
func (s *apiServer) databasesProxy(w http.ResponseWriter, r *http.Request) { s.proxy.ServeHTTP(w, r) }

func (s *apiServer) ListDatabases(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) CreateDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) GetDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) UpdateDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) DeleteDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.DeleteDatabaseParams) {
	s.databasesProxy(w, r)
}

func (s *apiServer) RotateDatabaseCredentials(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) ListDatabaseLinks(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) CreateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) DeleteDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi_types.UUID) {
	s.databasesProxy(w, r)
}

func (s *apiServer) RotateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi_types.UUID) {
	s.databasesProxy(w, r)
}

func (s *apiServer) ListDatabaseBackups(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) CreateDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) RestoreDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, backupId openapi_types.UUID) {
	s.databasesProxy(w, r)
}

func (s *apiServer) GetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) SetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	s.databasesProxy(w, r)
}

func (s *apiServer) GetDatabaseLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseLogsParams) {
	s.databasesProxy(w, r)
}

func (s *apiServer) GetDatabaseMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseMetricsParams) {
	s.databasesProxy(w, r)
}

func (s *apiServer) GetAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	s.databasesProxy(w, r)
}

func (s *apiServer) UpdateAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	s.databasesProxy(w, r)
}
