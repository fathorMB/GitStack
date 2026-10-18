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

func (s *apiServer) GetBranchSettings(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) UpdateBranchSettings(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	pullsNotImplemented(w)
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

func (s *apiServer) DeletePullRequestBranch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) ListPullRequestComments(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestCommentsParams) {
	pullsNotImplemented(w)
}

func (s *apiServer) CreatePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) DeletePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullCommentIdParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) UpdatePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullCommentIdParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) MergePullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) GetPullRequestMergeability(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) RequestPullRequestReviewers(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) RemovePullRequestReviewer(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.UsernameParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) ListPullRequestReviews(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestReviewsParams) {
	pullsNotImplemented(w)
}

func (s *apiServer) CreatePullRequestReview(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) ListPullRequestThreads(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestThreadsParams) {
	pullsNotImplemented(w)
}

func (s *apiServer) ResolvePullRequestThread(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullThreadIdParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) UnresolvePullRequestThread(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullThreadIdParam) {
	pullsNotImplemented(w)
}

func (s *apiServer) SetPullRequestFileViewed(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	pullsNotImplemented(w)
}
