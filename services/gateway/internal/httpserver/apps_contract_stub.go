package httpserver

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Le operazioni delle app (v3-a) sono servite da core; finche' gli item
// applicativi non le implementano, core risponde 501. Il gateway le inoltra
// comunque per applicare la sicurezza generata dal contratto.
func (s *apiServer) appsProxy(w http.ResponseWriter, r *http.Request) { s.proxy.ServeHTTP(w, r) }

func (s *apiServer) ListApps(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) CreateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) UpdateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeleteApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeployApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAppVersions(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.ListAppVersionsParams) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppVersion(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, version openapi.AppVersionParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RollbackApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RestartApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) ScaleApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAppVariables(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) PutAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeleteAppVariable(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAppSecrets(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) PutAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeleteAppSecret(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, name openapi.AppEnvNameParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) SetAppAccess(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) SetAppResources(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppLogsParams) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, params openapi.GetAppMetricsParams) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAdminApps(w http.ResponseWriter, r *http.Request, params openapi.ListAdminAppsParams) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAdminAppsCapacity(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) UpdateAdminAppsReserve(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAdminAppOwnerQuotas(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) PutAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeleteAdminAppOwnerQuota(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) UpdateAdminAppsSettings(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) ListAdminAppTrustedKeys(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) AddAdminAppTrustedKey(w http.ResponseWriter, r *http.Request) {
	s.appsProxy(w, r)
}

func (s *apiServer) DeleteAdminAppTrustedKey(w http.ResponseWriter, r *http.Request, keyId openapi.AppTrustedKeyIdParam) {
	s.appsProxy(w, r)
}

// Lista di accesso dell'app e invito ai tester (GIT-376, H6).
func (s *apiServer) ListAppAccessMembers(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) AddAppAccessMember(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) RemoveAppAccessMember(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, memberId openapi_types.UUID) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppAccessInvite(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	s.appsProxy(w, r)
}

func (s *apiServer) GetAppAccessMembership(w http.ResponseWriter, r *http.Request, appId openapi_types.UUID) {
	s.appsProxy(w, r)
}
