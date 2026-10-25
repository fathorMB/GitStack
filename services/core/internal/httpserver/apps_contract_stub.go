package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Le operazioni delle app (v3-a, H1-H13) sono fissate dal contratto GIT-341;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.

func (s *apiServer) ListApps(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam) {
	notImplemented(w)
}

func (s *apiServer) CreateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam) {
	notImplemented(w)
}

func (s *apiServer) GetApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) UpdateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) DeleteApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) DeployApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) ListAppVersions(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.ListAppVersionsParams) {
	notImplemented(w)
}

func (s *apiServer) GetAppVersion(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, version openapi.AppVersionParam) {
	notImplemented(w)
}

func (s *apiServer) RollbackApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) RestartApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) ScaleApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) ListAppVariables(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) PutAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	notImplemented(w)
}

func (s *apiServer) DeleteAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	notImplemented(w)
}

func (s *apiServer) ListAppSecrets(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	notImplemented(w)
}

func (s *apiServer) PutAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	notImplemented(w)
}

func (s *apiServer) DeleteAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	notImplemented(w)
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

func (s *apiServer) GetAppLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppLogsParams) {
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
