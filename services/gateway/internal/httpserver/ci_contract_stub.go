package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Le operazioni CI, registry e release sono servite da core; finche' gli item
// applicativi non le implementano, core risponde 501. Il gateway le inoltra
// comunque per applicare la sicurezza generata dal contratto.
func (s *apiServer) ciProxy(w http.ResponseWriter, r *http.Request) { s.proxy.ServeHTTP(w, r) }

func (s *apiServer) GetCiQueue(w http.ResponseWriter, r *http.Request)              { s.ciProxy(w, r) }
func (s *apiServer) GetCiStorageSettings(w http.ResponseWriter, r *http.Request)    { s.ciProxy(w, r) }
func (s *apiServer) UpdateCiStorageSettings(w http.ResponseWriter, r *http.Request) { s.ciProxy(w, r) }
func (s *apiServer) ListCiRunners(w http.ResponseWriter, r *http.Request)           { s.ciProxy(w, r) }
func (s *apiServer) CreateCiRunnerRegistrationToken(w http.ResponseWriter, r *http.Request) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteCiRunner(w http.ResponseWriter, r *http.Request, id openapi.RunnerIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListOrganizationActionsSecrets(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) PutOrganizationActionsSecret(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListOrganizationActionsVariables(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) PutOrganizationActionsVariable(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DownloadWorkflowArtifact(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ArtifactIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetWorkflowJob(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.JobIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetWorkflowJobLog(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.JobIdParam, p openapi.GetWorkflowJobLogParams) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListWorkflowRuns(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetWorkflowRun(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.RunIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListWorkflowArtifacts(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.RunIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) CancelWorkflowRun(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.RunIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) RerunWorkflowRun(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.RunIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListRepositoryActionsSecrets(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) PutRepositoryActionsSecret(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListRepositoryActionsVariables(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) PutRepositoryActionsVariable(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListRepositoryPackages(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) UpdateRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListReleases(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) CreateRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) UpdateRelease(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) UploadReleaseAsset(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam, params openapi.UploadReleaseAssetParams) {
	s.ciProxy(w, r)
}
func (s *apiServer) DownloadReleaseAsset(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ReleaseIdParam, asset openapi.AssetIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListWorkflows(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DispatchWorkflow(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, workflow openapi.WorkflowParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) ListActionsCaches(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, params openapi.ListActionsCachesParams) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteActionsCaches(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, params openapi.DeleteActionsCachesParams) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetActionsCache(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.CacheIdParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteActionsCache(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.CacheIdParam) {
	s.ciProxy(w, r)
}

func (s *apiServer) DeleteOrganizationActionsSecret(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, name openapi.ActionsNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteOrganizationActionsVariable(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, name openapi.ActionsNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteRepositoryActionsSecret(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, name openapi.ActionsNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) DeleteRepositoryActionsVariable(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, name openapi.ActionsNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) GetRepositoryActionsPermissions(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
func (s *apiServer) PutRepositoryActionsPermissions(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	s.ciProxy(w, r)
}
