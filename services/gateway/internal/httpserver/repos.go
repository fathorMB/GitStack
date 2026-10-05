package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Operazioni del tag `repos` (M-03): servite da core, il gateway le instrada
// con l'handler condiviso. GET /v1/repos/deleted (2 segmenti) e
// GET /v1/repos/{owner}/{repo} (3) non si sovrappongono: vedi router_test.

func (s *apiServer) ListRepositories(w http.ResponseWriter, r *http.Request, _ openapi.ListRepositoriesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateRepository(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListDeletedRepositories(w http.ResponseWriter, r *http.Request, _ openapi.ListDeletedRepositoriesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) RestoreRepository(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetRepository(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateRepository(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteRepository(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

// GetUserAccess (GET /v1/users/{username}/access) è servita da core, anche se
// il percorso sta sotto /users: il servizio lo decide la tabella di sicurezza
// generata dal contratto (tag `repos`).
func (s *apiServer) GetUserAccess(w http.ResponseWriter, r *http.Request, _ openapi.UsernameParam, _ openapi.GetUserAccessParams) {
	s.proxy.ServeHTTP(w, r)
}
