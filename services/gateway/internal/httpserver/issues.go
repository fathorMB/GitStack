package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// Issues (M-05, GIT-101): servite da core, il gateway le instrada con
// l'handler condiviso (le dichiarazioni di sicurezza vengono dal contratto).

func (s *apiServer) SearchIssues(w http.ResponseWriter, r *http.Request, _ openapi.SearchIssuesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssues(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListIssuesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CloseIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ReopenIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetIssueHidden(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) LockIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UnlockIssue(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetIssueAssignees(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetIssueLabels(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) SetIssueMilestone(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssueEvents(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.ListIssueEventsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssueVersions(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssueComments(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.ListIssueCommentsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateIssueComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateIssueComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteIssueComment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssueCommentVersions(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.IssueCommentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UploadIssueAttachment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetIssueAttachment(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueAttachmentIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListIssueTemplates(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListLabels(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListLabelsParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateLabel(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetLabel(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateLabel(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteLabel(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) ListMilestones(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListMilestonesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateMilestone(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetMilestone(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateMilestone(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteMilestone(w http.ResponseWriter, r *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	s.proxy.ServeHTTP(w, r)
}
