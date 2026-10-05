// Package coreevents definisce gli eventi di dominio che il servizio core
// pubblica su NATS (M-06, GIT-129/GIT-130): issue.*, issue_comment.* e
// repository.*, tutti a versione 1. Il contratto, con tabelle e consumer, è
// in docs/events.md, sezione «Eventi di dominio di core (M-06)».
//
// I payload sono istantanee minime: gli id e i campi che servono a decidere
// chi notificare e a scrivere il testo; il dettaglio completo si legge dal
// database di core. Chi consuma registra i decoder con Register.
package coreevents

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/fathorMB/GitStack/pkg/events"
)

// Version è la versione corrente dello schema di tutti gli eventi di dominio.
const Version = 1

// Domini (primo segmento del nome, subject e stream ISSUE, ISSUE_COMMENT,
// REPOSITORY).
const (
	DomainIssue        = "issue"
	DomainIssueComment = "issue_comment"
	DomainRepository   = "repository"
)

// Domains elenca i domini di core, da assicurare con events.EnsureStream.
var Domains = []string{DomainIssue, DomainIssueComment, DomainRepository}

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

func strict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// DecodeIssue valida e decodifica il payload v1 di issue.*.
func DecodeIssue(raw []byte) (any, error) {
	var p IssuePayload
	if err := strict(raw, &p); err != nil {
		return nil, fmt.Errorf("coreevents: decode issue v%d: %w", Version, err)
	}
	if p.Repo.ID == "" || p.Issue.ID == "" {
		return nil, fmt.Errorf("coreevents: payload issue v%d senza repo.id o issue.id", Version)
	}
	return p, nil
}

// DecodeIssueComment valida e decodifica il payload v1 di issue_comment.*.
func DecodeIssueComment(raw []byte) (any, error) {
	var p IssueCommentPayload
	if err := strict(raw, &p); err != nil {
		return nil, fmt.Errorf("coreevents: decode issue_comment v%d: %w", Version, err)
	}
	if p.Repo.ID == "" || p.Issue.ID == "" || p.Comment.ID == "" {
		return nil, fmt.Errorf("coreevents: payload issue_comment v%d senza repo.id, issue.id o comment.id", Version)
	}
	return p, nil
}

// DecodeRepository valida e decodifica il payload v1 di repository.*.
func DecodeRepository(raw []byte) (any, error) {
	var p RepositoryPayload
	if err := strict(raw, &p); err != nil {
		return nil, fmt.Errorf("coreevents: decode repository v%d: %w", Version, err)
	}
	if p.Repo.ID == "" {
		return nil, fmt.Errorf("coreevents: payload repository v%d senza repo.id", Version)
	}
	return p, nil
}

// Register registra su reg i decoder di tutti gli eventi di core: da
// chiamare nei consumer (notifiche, webhook).
func Register(reg *events.Registry) {
	for _, n := range IssueNames {
		reg.Register(n, Version, DecodeIssue)
	}
	for _, n := range IssueCommentNames {
		reg.Register(n, Version, DecodeIssueComment)
	}
	for _, n := range RepositoryNames {
		reg.Register(n, Version, DecodeRepository)
	}
}
