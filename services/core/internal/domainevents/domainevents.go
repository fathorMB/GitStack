// Package domainevents sono i nomi e i payload degli eventi di dominio che core
// pubblica (M-06/B, GIT-130). Gli schemi di riferimento, con i decoder per i
// consumer, sono in pkg/events/coreevents: qui ci sono copie dei soli tipi,
// perché core si compila fuori dal workspace (Dockerfile) contro una
// pseudo-versione di pkg/events che non contiene ancora quel pacchetto. Un
// test d'integrazione (events_integration_test.go di httpserver) decodifica
// ogni evento pubblicato con i decoder di pkg/events/coreevents: se le due
// copie divergono, il test diventa rosso.
package domainevents

import "encoding/json"

// Version è la versione corrente dello schema di tutti gli eventi di dominio.
const Version = 1

// Domini (primo segmento del nome, subject e stream ISSUE, ISSUE_COMMENT,
// REPOSITORY).
const (
	DomainIssue        = "issue"
	DomainIssueComment = "issue_comment"
	DomainRepository   = "repository"

	// v1.1 (GIT-265, PR11): Pull Request. Un dominio per ogni primo segmento.
	DomainPullRequest        = "pull_request"
	DomainPullRequestReview  = "pull_request_review"
	DomainPullRequestComment = "pull_request_comment"
	DomainCommitStatus       = "commit_status"

	// v2-a (GIT-281, CI6): esecuzioni e job dei workflow.
	DomainWorkflowRun = "workflow_run"
	DomainWorkflowJob = "workflow_job"

	// v3-a (GIT-384, H12): deploy delle app e stato «fermata» (copie non pronte dopo una partenza).
	DomainAppDeployment = "app_deployment"
	DomainAppStatus     = "app_status"
)

// Domains elenca i domini di core, da assicurare con events.EnsureStream.
var Domains = []string{DomainIssue, DomainIssueComment, DomainRepository,
	DomainPullRequest, DomainPullRequestReview, DomainPullRequestComment, DomainCommitStatus,
	DomainWorkflowRun, DomainWorkflowJob, DomainAppDeployment, DomainAppStatus, DomainAppDomain, DomainAppCertificate}

// Nomi degli eventi (anche subject NATS).
const (
	IssueCreated      = "issue.created"
	IssueEdited       = "issue.edited"
	IssueClosed       = "issue.closed"
	IssueReopened     = "issue.reopened"
	IssueAssigned     = "issue.assigned"
	IssueUnassigned   = "issue.unassigned"
	IssueLabeled      = "issue.labeled"
	IssueUnlabeled    = "issue.unlabeled"
	IssueMilestoned   = "issue.milestoned"
	IssueDemilestoned = "issue.demilestoned"
	IssueLocked       = "issue.locked"
	IssueUnlocked     = "issue.unlocked"
	IssueHidden       = "issue.hidden"
	IssueUnhidden     = "issue.unhidden"

	IssueCommentCreated = "issue_comment.created"
	IssueCommentEdited  = "issue_comment.edited"
	IssueCommentDeleted = "issue_comment.deleted"

	RepositoryCreated           = "repository.created"
	RepositoryDeleted           = "repository.deleted"
	RepositoryRestored          = "repository.restored"
	RepositoryArchived          = "repository.archived"
	RepositoryUnarchived        = "repository.unarchived"
	RepositoryVisibilityChanged = "repository.visibility_changed"
)

// IssueNames, IssueCommentNames e RepositoryNames sono gli eventi per dominio.
var (
	IssueNames = []string{IssueCreated, IssueEdited, IssueClosed, IssueReopened, IssueAssigned, IssueUnassigned,
		IssueLabeled, IssueUnlabeled, IssueMilestoned, IssueDemilestoned, IssueLocked, IssueUnlocked, IssueHidden, IssueUnhidden}
	IssueCommentNames = []string{IssueCommentCreated, IssueCommentEdited, IssueCommentDeleted}
	RepositoryNames   = []string{RepositoryCreated, RepositoryDeleted, RepositoryRestored, RepositoryArchived,
		RepositoryUnarchived, RepositoryVisibilityChanged}
)

// Tipi di utente (Actor.Type).
const (
	UserHuman = "human"
	UserAgent = "agent"
)

// Repo è il repo come in core al momento dell'evento.
type Repo struct {
	ID            string `json:"id"`
	FullName      string `json:"fullName"` // owner/repo
	DefaultBranch string `json:"defaultBranch"`
	Visibility    string `json:"visibility"`
	Archived      bool   `json:"archived"`
}

// User è un utente: chi ha agito (Actor) o un assegnatario.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Type     string `json:"type"` // human | agent
}

// Issue è la issue dopo il cambiamento.
type Issue struct {
	ID          string   `json:"id"`
	Number      int64    `json:"number"`
	Title       string   `json:"title"`
	State       string   `json:"state"` // open | closed
	AuthorID    string   `json:"authorId"`
	AssigneeIDs []string `json:"assigneeIds"`
	Hidden      bool     `json:"hidden"`
	Locked      bool     `json:"locked"`
}

// From è il valore precedente di un campo modificato.
type From struct {
	From string `json:"from"`
}

// IssueChanges: i campi cambiati da issue.edited, col valore precedente.
type IssueChanges struct {
	Title *From `json:"title,omitempty"`
	Body  *From `json:"body,omitempty"`
}

// Label, Milestone e Commit compaiono in alcuni eventi issue.
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Milestone struct {
	ID     string `json:"id"`
	Number int64  `json:"number"`
	Title  string `json:"title"`
}

type Commit struct {
	SHA        string `json:"sha"`
	Repository string `json:"repository"`
}

// IssuePayload è lo schema v1 degli eventi issue.*. Actor è nil per gli
// eventi di sistema. Mentions: user id menzionati (assente = nessuno).
type IssuePayload struct {
	Repo        Repo          `json:"repo"`
	Actor       *User         `json:"actor"`
	Issue       Issue         `json:"issue"`
	Mentions    []string      `json:"mentions,omitempty"`
	Changes     *IssueChanges `json:"changes,omitempty"`
	Reason      string        `json:"reason,omitempty"` // closed: completed|not_planned|duplicate; locked: testo libero
	DuplicateOf *int64        `json:"duplicateOf,omitempty"`
	Commit      *Commit       `json:"commit,omitempty"`
	Assignee    *User         `json:"assignee,omitempty"`
	Label       *Label        `json:"label,omitempty"`
	Milestone   *Milestone    `json:"milestone,omitempty"`
}

// Comment è il commento; Body manca in issue_comment.deleted (I4).
type Comment struct {
	ID       string `json:"id"`
	AuthorID string `json:"authorId"`
	Body     string `json:"body,omitempty"`
}

// CommentChanges: il testo precedente di issue_comment.edited.
type CommentChanges struct {
	Body *From `json:"body,omitempty"`
}

// IssueCommentPayload è lo schema v1 degli eventi issue_comment.*.
type IssueCommentPayload struct {
	Repo     Repo            `json:"repo"`
	Actor    *User           `json:"actor"`
	Issue    Issue           `json:"issue"`
	Comment  Comment         `json:"comment"`
	Mentions []string        `json:"mentions,omitempty"`
	Changes  *CommentChanges `json:"changes,omitempty"`
}

// RepositoryChanges: la visibilità precedente di repository.visibility_changed.
type RepositoryChanges struct {
	Visibility *From `json:"visibility,omitempty"`
}

// RepositoryPayload è lo schema v1 degli eventi repository.*. DeletedAt e
// PurgeAt (RFC 3339, purge = 7 giorni dopo, R2) solo per repository.deleted.
type RepositoryPayload struct {
	Repo      Repo               `json:"repo"`
	Actor     *User              `json:"actor"`
	Changes   *RepositoryChanges `json:"changes,omitempty"`
	DeletedAt string             `json:"deletedAt,omitempty"`
	PurgeAt   string             `json:"purgeAt,omitempty"`
}

// Eventi delle Pull Request (v1.1, GIT-265, PR11). Come gli altri passano
// dall'outbox transazionale: notifiche e webhook li leggono da lì.
const (
	PullRequestOpened               = "pull_request.opened"
	PullRequestEdited               = "pull_request.edited"
	PullRequestClosed               = "pull_request.closed"
	PullRequestReopened             = "pull_request.reopened"
	PullRequestMerged               = "pull_request.merged"
	PullRequestReadyForReview       = "pull_request.ready_for_review"
	PullRequestConvertedToDraft     = "pull_request.converted_to_draft"
	PullRequestSynchronize          = "pull_request.synchronize"
	PullRequestReviewRequested      = "pull_request.review_requested"
	PullRequestReviewRequestRemoved = "pull_request.review_request_removed"
	// PullRequestConflicts e PullRequestChecksFailed non hanno un webhook:
	// avvisano solo l'autore (notifiche, PR11).
	PullRequestConflicts    = "pull_request.conflicts"
	PullRequestChecksFailed = "pull_request.checks_failed"

	PullRequestReviewSubmitted = "pull_request_review.submitted"
	PullRequestReviewDismissed = "pull_request_review.dismissed"

	// PullRequestCommentCreated: commento generale o sulle righe. Solo
	// notifiche: nessun webhook (i commenti sulle righe viaggiano nella review).
	PullRequestCommentCreated = "pull_request_comment.created"

	CommitStatusCreated = "commit_status.created"
)

// PullRequestNames sono gli eventi del dominio pull_request.
var PullRequestNames = []string{PullRequestOpened, PullRequestEdited, PullRequestClosed, PullRequestReopened,
	PullRequestMerged, PullRequestReadyForReview, PullRequestConvertedToDraft, PullRequestSynchronize,
	PullRequestReviewRequested, PullRequestReviewRequestRemoved, PullRequestConflicts, PullRequestChecksFailed}

// Pull è la PR dopo il cambiamento, ridotta a ciò che serve a notifiche e
// consumatori; la PR completa del contratto sta in PullPayload.Snapshot.
type Pull struct {
	ID           string `json:"id"`
	Number       int64  `json:"number"`
	Title        string `json:"title"`
	State        string `json:"state"` // open | closed | merged
	Draft        bool   `json:"draft"`
	AuthorID     string `json:"authorId"`
	SourceBranch string `json:"sourceBranch"`
	TargetBranch string `json:"targetBranch"`
	HeadSha      string `json:"headSha,omitempty"`
}

// PullReview è la review di pull_request_review.*.
type PullReview struct {
	ID       string `json:"id"`
	AuthorID string `json:"authorId"`
	State    string `json:"state"` // comment | approve | request_changes
	Body     string `json:"body,omitempty"`
}

// PullPayload è lo schema v1 degli eventi pull_request.*, pull_request_review.*
// e pull_request_comment.created. Snapshot è la PR come in GET …/pulls/{n}
// (schema PullRequest del contratto) al momento dell'evento: i webhook la
// consegnano così com'è. ReviewSnapshot è la review del contratto.
type PullPayload struct {
	Repo              Repo            `json:"repo"`
	Actor             *User           `json:"actor"`
	Pull              Pull            `json:"pull"`
	Snapshot          json.RawMessage `json:"snapshot,omitempty"`
	Changes           *IssueChanges   `json:"changes,omitempty"`
	RequestedReviewer *User           `json:"requestedReviewer,omitempty"`
	Review            *PullReview     `json:"review,omitempty"`
	ReviewSnapshot    json.RawMessage `json:"reviewSnapshot,omitempty"`
	Comment           *Comment        `json:"comment,omitempty"`
	// Reason: per pull_request.edited, "retargeted" se è cambiata la destinazione.
	Reason string `json:"reason,omitempty"`
	// Contexts: i contesti obbligatori in errore di pull_request.checks_failed.
	Contexts []string `json:"contexts,omitempty"`
	// Files: i file in conflitto di pull_request.conflicts (al massimo 10).
	Files []string `json:"files,omitempty"`
}

// Eventi delle esecuzioni e dei job (GIT-281). Le azioni dei webhook sono
// queued, in_progress, completed e cancelled, come nel contratto.
const (
	WorkflowRunQueued     = "workflow_run.queued"
	WorkflowRunInProgress = "workflow_run.in_progress"
	WorkflowRunCompleted  = "workflow_run.completed"
	WorkflowRunCancelled  = "workflow_run.cancelled"

	WorkflowJobQueued     = "workflow_job.queued"
	WorkflowJobInProgress = "workflow_job.in_progress"
	WorkflowJobCompleted  = "workflow_job.completed"
	WorkflowJobCancelled  = "workflow_job.cancelled"
)

// WorkflowRunPayload è lo schema v1 di workflow_run.*. Run è il WorkflowRun del
// contratto com'è al momento dell'evento (i webhook lo consegnano così com'è);
// Previous è la conclusione dell'esecuzione precedente dello stesso workflow e
// ref, vuota se non c'è: serve a notificare il ritorno al verde (workflow_run.completed).
type WorkflowRunPayload struct {
	Repo     Repo            `json:"repo"`
	Actor    *User           `json:"actor"`
	Run      json.RawMessage `json:"run"`
	Previous string          `json:"previousConclusion,omitempty"`
}

// WorkflowJobPayload è lo schema v1 di workflow_job.*: Run è l'esecuzione e Job
// il WorkflowJob del contratto, entrambi al momento dell'evento.
type WorkflowJobPayload struct {
	Repo  Repo            `json:"repo"`
	Actor *User           `json:"actor"`
	Run   json.RawMessage `json:"run"`
	Job   json.RawMessage `json:"job"`
}

// CommitStatusPayload è lo schema v1 di commit_status.created. Status è il
// CommitStatus del contratto; Branches i branch la cui punta è il commit.
type CommitStatusPayload struct {
	Repo     Repo            `json:"repo"`
	Actor    *User           `json:"actor"`
	Status   json.RawMessage `json:"status"`
	Sha      string          `json:"sha"`
	Context  string          `json:"context"`
	State    string          `json:"state"`
	Combined string          `json:"combinedState"`
	Branches []string        `json:"branches"`
}
