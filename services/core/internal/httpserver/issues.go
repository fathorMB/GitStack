package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Issues (M-05, GIT-101): il contratto e lo schema (migrazione 0004) sono
// fissati, le operazioni rispondono 501 finché gli item a valle non le
// implementano. Regole I1-I11: README di core.
func issuesNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Issues non ancora disponibili.")
}

func (s *apiServer) SearchIssues(w http.ResponseWriter, _ *http.Request, _ openapi.SearchIssuesParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) LockIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UnlockIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueComments(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.ListIssueCommentsParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) CreateIssueComment(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UpdateIssueComment(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) DeleteIssueComment(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueCommentVersions(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueTemplates(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

