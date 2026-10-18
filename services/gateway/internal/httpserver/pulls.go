package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Pull Request, review, stati dei commit e impostazioni «Branches & merge»
// (v1.1, GIT-258): servite da core, il gateway le instrada con l'handler
// condiviso (le dichiarazioni di sicurezza vengono dal contratto).

func (s *apiServer) GetBranchSettings(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateBranchSettings(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetCombinedCommitStatus(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListCommitStatuses(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam, _ openapi.ListCommitStatusesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateCommitStatus(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.CommitShaParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequests(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListPullRequestsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreatePullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetPullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdatePullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeletePullRequestBranch(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ClosePullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequestComments(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestCommentsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreatePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeletePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullCommentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdatePullRequestComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullCommentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequestCommits(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestCommitsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) MarkPullRequestDraft(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequestFiles(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestFilesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) MergePullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetPullRequestMergeability(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) MarkPullRequestReady(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ReopenPullRequest(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) RequestPullRequestReviewers(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) RemovePullRequestReviewer(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.UsernameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequestReviews(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestReviewsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreatePullRequestReview(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListPullRequestThreads(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.ListPullRequestThreadsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ResolvePullRequestThread(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullThreadIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UnresolvePullRequestThread(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam, _ openapi.PullThreadIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetPullRequestFileViewed(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.PullNumberParam) {
	s.proxy.ServeHTTP(w, r)
}
