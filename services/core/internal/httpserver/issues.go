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

func (s *apiServer) ListIssues(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListIssuesParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) CreateIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) GetIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UpdateIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) CloseIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ReopenIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) SetIssueHidden(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) LockIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UnlockIssue(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) SetIssueAssignees(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) SetIssueLabels(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) SetIssueMilestone(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueEvents(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam, _ openapi.ListIssueEventsParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueVersions(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueNumberParam) {
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

func (s *apiServer) UploadIssueAttachment(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) GetIssueAttachment(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.IssueAttachmentIdParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListIssueTemplates(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListLabels(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListLabelsParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) CreateLabel(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) GetLabel(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UpdateLabel(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) DeleteLabel(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.LabelNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) ListMilestones(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.ListMilestonesParams) {
	issuesNotImplemented(w)
}

func (s *apiServer) CreateMilestone(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) GetMilestone(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) UpdateMilestone(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	issuesNotImplemented(w)
}

func (s *apiServer) DeleteMilestone(w http.ResponseWriter, _ *http.Request, _ openapi.RepoOwnerParam, _ openapi.RepoNameParam, _ openapi.MilestoneNumberParam) {
	issuesNotImplemented(w)
}
