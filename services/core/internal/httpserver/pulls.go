package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Pull Request, review, stati dei commit e impostazioni «Branches & merge»
// (v1.1, GIT-258): il contratto (api/openapi.yaml, tag pulls) e' fissato, le
// operazioni rispondono 501 finche' gli item a valle (GIT-261 e seguenti) non
// le implementano. Regole PR1-PR12.

func pullsNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "operazione prevista dalla v1.1, non ancora implementata")
}

func (s *apiServer) GetCombinedCommitStatus(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) ListCommitStatuses(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.ListCommitStatusesParams) {
	pullsNotImplemented(w)
}

func (s *apiServer) CreateCommitStatus(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) DeletePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullCommentIdParam) {
	pullsNotImplemented(w)
}

