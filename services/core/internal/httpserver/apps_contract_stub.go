package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Le operazioni delle app (v3-a, H1-H13) sono fissate dal contratto GIT-341;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.
func notImplemented(w http.ResponseWriter) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *apiServer) GetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) SetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) GetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) SetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) GetAppMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppMetricsParams) {
	notImplemented(w)
}

func (s *apiServer) ListAdminApps(w http.ResponseWriter, r *http.Request, params openapi.ListAdminAppsParams) {
	notImplemented(w)
}

func (s *apiServer) GetAdminAppsCapacity(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) UpdateAdminAppsReserve(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) ListAdminAppOwnerQuotas(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) PutAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	notImplemented(w)
}

func (s *apiServer) DeleteAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	notImplemented(w)
}

func (s *apiServer) GetAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) UpdateAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) ListAdminAppTrustedKeys(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) AddAdminAppTrustedKey(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (s *apiServer) DeleteAdminAppTrustedKey(w http.ResponseWriter, r *http.Request, keyId openapi.AppTrustedKeyIdParam) {
	notImplemented(w)
}
