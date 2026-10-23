package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Le superfici CI, registry e release sono fissate dal contratto GIT-275;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.
func notImplemented(w http.ResponseWriter) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (s *apiServer) GetCiQueue(w http.ResponseWriter, r *http.Request)    { notImplemented(w) }
func (s *apiServer) ListCiRunners(w http.ResponseWriter, r *http.Request) { notImplemented(w) }
func (s *apiServer) CreateCiRunnerRegistrationToken(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}
func (s *apiServer) DeleteCiRunner(w http.ResponseWriter, r *http.Request, id openapi.RunnerIdParam) {
	notImplemented(w)
}
func (s *apiServer) DownloadWorkflowArtifact(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.ArtifactIdParam) {
	notImplemented(w)
}
func (s *apiServer) ListWorkflowArtifacts(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.RunIdParam) {
	notImplemented(w)
}
func (s *apiServer) ListRepositoryPackages(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	notImplemented(w)
}
func (s *apiServer) GetRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	notImplemented(w)
}
func (s *apiServer) UpdateRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	notImplemented(w)
}
func (s *apiServer) ListActionsCaches(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, params openapi.ListActionsCachesParams) {
	notImplemented(w)
}
func (s *apiServer) DeleteActionsCaches(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, params openapi.DeleteActionsCachesParams) {
	notImplemented(w)
}
func (s *apiServer) GetActionsCache(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.CacheIdParam) {
	notImplemented(w)
}
func (s *apiServer) DeleteActionsCache(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam, id openapi.CacheIdParam) {
	notImplemented(w)
}
