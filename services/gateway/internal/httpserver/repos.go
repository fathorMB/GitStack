package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Operazioni del tag `repos` (M-03): servite da core, il gateway le instrada
// con l'handler condiviso. Eliminazione definitiva (R2, GIT-530): niente cestino né ripristino.

func (s *apiServer) ListRepositories(w http.ResponseWriter, r *http.Request, _ openapi.ListRepositoriesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateRepository(w http.ResponseWriter, r *http.Request) {
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
