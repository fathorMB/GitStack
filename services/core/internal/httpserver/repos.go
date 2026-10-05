package httpserver

import (
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Operazioni del tag `repos` (M-03). Il contratto e lo schema (migrazione
// 0002) sono di GIT-63; gli handler arrivano con GIT-67: fino ad allora
// rispondono 501.

func repoNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Operazione sui repo non ancora disponibile.")
}

func (s *apiServer) ListRepositories(w http.ResponseWriter, _ *http.Request, _ openapi.ListRepositoriesParams) {
	repoNotImplemented(w)
}

func (s *apiServer) CreateRepository(w http.ResponseWriter, _ *http.Request) {
	repoNotImplemented(w)
}

func (s *apiServer) ListDeletedRepositories(w http.ResponseWriter, _ *http.Request, _ openapi.ListDeletedRepositoriesParams) {
	repoNotImplemented(w)
}

func (s *apiServer) RestoreRepository(w http.ResponseWriter, _ *http.Request, _ openapi_types.UUID) {
	repoNotImplemented(w)
}

func (s *apiServer) GetRepository(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	repoNotImplemented(w)
}

func (s *apiServer) UpdateRepository(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	repoNotImplemented(w)
}

func (s *apiServer) DeleteRepository(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	repoNotImplemented(w)
}
