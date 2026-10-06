package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tipi di webhook (X-GitStack-Event).
const (
	EventPush         = "push"
	EventIssues       = "issues"
	EventIssueComment = "issue_comment"
	EventRepository   = "repository"
)

// Events sono gli eventi selezionabili.
var Events = []string{EventPush, EventIssues, EventIssueComment, EventRepository}

// issueActions: evento di dominio → action di `issues`. issue.hidden e
// issue.unhidden non hanno webhook (docs/events.md).
var issueActions = map[string]string{
	domainevents.IssueCreated: "opened", domainevents.IssueEdited: "edited", domainevents.IssueClosed: "closed",
	domainevents.IssueReopened: "reopened", domainevents.IssueAssigned: "assigned", domainevents.IssueUnassigned: "unassigned",
	domainevents.IssueLabeled: "labeled", domainevents.IssueUnlabeled: "unlabeled", domainevents.IssueMilestoned: "milestoned",
	domainevents.IssueDemilestoned: "demilestoned", domainevents.IssueLocked: "locked", domainevents.IssueUnlocked: "unlocked",
}

var commentActions = map[string]string{
	domainevents.IssueCommentCreated: "created", domainevents.IssueCommentEdited: "edited", domainevents.IssueCommentDeleted: "deleted",
}

var repositoryActions = map[string]string{
	domainevents.RepositoryCreated: "created", domainevents.RepositoryDeleted: "deleted", domainevents.RepositoryRestored: "restored",
	domainevents.RepositoryArchived: "archived", domainevents.RepositoryUnarchived: "unarchived",
	domainevents.RepositoryVisibilityChanged: "visibility_changed",
}

// obj è un oggetto JSON con le chiavi nell'ordine di inserimento sulla
// serializzazione di encoding/json (mappa: ordine alfabetico, stabile).
type obj = map[string]any

func (e *Engine) handle(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	if a, ok := issueActions[ev.name]; ok {
		var p domainevents.IssuePayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return &badEventError{err}
		}
		return e.dispatch(ctx, tx, ev, EventIssues, a, p.Repo, p.Actor, p.Issue.Hidden, func(b *builder) error {
			return b.issuePayload(ctx, tx, p)
		})
	}
	if a, ok := commentActions[ev.name]; ok {
		var p domainevents.IssueCommentPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return &badEventError{err}
		}
		return e.dispatch(ctx, tx, ev, EventIssueComment, a, p.Repo, p.Actor, p.Issue.Hidden, func(b *builder) error {
			return b.commentPayload(ctx, tx, p)
		})
	}
	if a, ok := repositoryActions[ev.name]; ok {
		var p domainevents.RepositoryPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return &badEventError{err}
		}
		return e.dispatch(ctx, tx, ev, EventRepository, a, p.Repo, p.Actor, false, func(b *builder) error {
			b.repositoryPayload(p)
			return nil
		})
	}
	return nil // issue.hidden, issue.unhidden e gli eventi che non sono webhook
}

// dispatch trova i webhook interessati e, se ce ne sono, costruisce il payload
// una sola volta e crea le consegne. Una issue nascosta (e i suoi commenti)
// non ha webhook (I4).
func (e *Engine) dispatch(ctx context.Context, tx pgx.Tx, ev outboxEvent, event, action string, repo domainevents.Repo,
	actor *domainevents.User, hidden bool, fill func(*builder) error) error {
	if hidden {
		return nil
	}
	repoID, err := uuid.Parse(repo.ID)
	if err != nil {
		return &badEventError{err}
	}
	targets, err := matching(ctx, tx, repoID, event)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	b := &builder{e: e, body: obj{}}
	b.common(event, action, ev.created)
	if err := b.repository(ctx, tx, repo); err != nil {
		return err
	}
	if err := b.sender(ctx, actor); err != nil {
		return err
	}
	if err := fill(b); err != nil {
		return err
	}
	raw, err := json.Marshal(b.body)
	if err != nil {
		return &badEventError{err}
	}
	return e.enqueue(ctx, tx, targets, event, action, ev.id, raw)
}

// builder compone un payload v1 (docs/webhooks.md).
type builder struct {
	e    *Engine
	body obj
	// users: cache degli utenti risolti da identity per questo evento.
	users map[uuid.UUID]userRef
}

type userRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Type     string `json:"type"`
}

func (b *builder) common(event, action string, at time.Time) {
	b.body["version"] = PayloadVersion
	b.body["event"] = event
	if action != "" {
		b.body["action"] = action
	}
	b.body["time"] = at.UTC().Format(time.RFC3339)
}

// repository scrive repository e organization: lo stato del repo è quello
// dell'evento, l'organizzazione si legge dal database.
func (b *builder) repository(ctx context.Context, tx pgx.Tx, repo domainevents.Repo) error {
	owner, name, _ := strings.Cut(repo.FullName, "/")
	b.body["repository"] = obj{
		"id": repo.ID, "fullName": repo.FullName, "owner": owner, "name": name,
		"defaultBranch": repo.DefaultBranch, "visibility": repo.Visibility, "archived": repo.Archived,
	}
	id, err := uuid.Parse(repo.ID)
	if err != nil {
		return &badEventError{err}
	}
	var ownerType, ownerName string
	var ownerID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT owner_type, owner_id, owner_name FROM core.repositories WHERE resource_id = $1`, id).
		Scan(&ownerType, &ownerID, &ownerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ownerType == "organization" {
		b.body["organization"] = obj{"id": ownerID.String(), "name": ownerName}
	}
	return nil
}

// resolve completa nome e tipo degli utenti con identity (se c'è); un utente
// non risolto resta con i dati dell'evento.
func (b *builder) resolve(ctx context.Context, ids ...string) error {
	if b.users == nil {
		b.users = map[uuid.UUID]userRef{}
	}
	var need []uuid.UUID
	for _, s := range ids {
		id, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		if _, ok := b.users[id]; ok {
			continue
		}
		b.users[id] = userRef{ID: id.String(), Type: "human"}
		need = append(need, id)
	}
	if len(need) == 0 || b.e.Users == nil {
		return nil
	}
	found, err := b.e.Users.LookupUsers(ctx, need)
	if err != nil {
		return fmt.Errorf("risoluzione degli utenti: %w", err)
	}
	for id, u := range found {
		b.users[id] = userRef{ID: id.String(), Username: u.Username, Type: u.Kind}
	}
	return nil
}

func (b *builder) user(id string) any {
	u, err := uuid.Parse(id)
	if err != nil {
		return nil
	}
	if r, ok := b.users[u]; ok {
		return r
	}
	return userRef{ID: id, Type: "human"}
}

// sender: chi ha causato l'evento; null per gli eventi di sistema.
func (b *builder) sender(ctx context.Context, actor *domainevents.User) error {
	if actor == nil || actor.ID == "" {
		b.body["sender"] = nil
		return nil
	}
	u, err := b.userFromEvent(ctx, actor)
	b.body["sender"] = u
	return err
}

func (b *builder) userFromEvent(ctx context.Context, u *domainevents.User) (any, error) {
	if u == nil {
		return nil, nil
	}
	if err := b.resolve(ctx, u.ID); err != nil {
		return nil, err
	}
	id, _ := uuid.Parse(u.ID)
	r := b.users[id]
	// Identity non lo ha risolto (o non c'è): valgono i dati dell'evento.
	if r.Username == "" {
		r.Username = u.Username
		if u.Type != "" {
			r.Type = u.Type
		}
	}
	return r, nil
}

func (b *builder) repositoryPayload(p domainevents.RepositoryPayload) {
	if p.Changes != nil && p.Changes.Visibility != nil {
		b.body["changes"] = obj{"visibility": obj{"from": p.Changes.Visibility.From}}
	}
	if p.DeletedAt != "" {
		b.body["deletedAt"] = p.DeletedAt
	}
}

type labelRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// issuePayload: lo stato completo della issue letto dal database. Se la issue
// non c'è più (eliminazione definitiva del repo) l'evento non ha più senso.
func (b *builder) issuePayload(ctx context.Context, tx pgx.Tx, p domainevents.IssuePayload) error {
	id, err := uuid.Parse(p.Issue.ID)
	if err != nil {
		return &badEventError{err}
	}
	var (
		number                                       int64
		title, body, state                           string
		closeReason                                  *string
		duplicateOf                                  *int64
		authorID                                     uuid.UUID
		milestoneID                                  *uuid.UUID
		locked                                       bool
		createdAt, updatedAt                         time.Time
		closedAt                                     *time.Time
		msNumber                                     *int64
		msTitle                                      *string
	)
	err = tx.QueryRow(ctx, `SELECT i.number, i.title, i.body, i.state, i.close_reason, i.duplicate_of, i.author_id, i.milestone_id,
			i.locked, i.created_at, i.updated_at, i.closed_at, m.number, m.title
		FROM core.issues i LEFT JOIN core.milestones m ON m.id = i.milestone_id WHERE i.id = $1`, id).
		Scan(&number, &title, &body, &state, &closeReason, &duplicateOf, &authorID, &milestoneID,
			&locked, &createdAt, &updatedAt, &closedAt, &msNumber, &msTitle)
	if errors.Is(err, pgx.ErrNoRows) {
		return &badEventError{errors.New("la issue non esiste più")}
	}
	if err != nil {
		return err
	}
	labels := []labelRow{}
	rows, err := tx.Query(ctx, `SELECT l.id, l.name, l.color FROM core.issue_labels il JOIN core.labels l ON l.id = il.label_id
		WHERE il.issue_id = $1 ORDER BY lower(l.name), l.id`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var l labelRow
		var lid uuid.UUID
		if err := rows.Scan(&lid, &l.Name, &l.Color); err != nil {
			rows.Close()
			return err
		}
		l.ID = lid.String()
		labels = append(labels, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	assigneeIDs := []string{}
	arows, err := tx.Query(ctx, `SELECT user_id FROM core.issue_assignees WHERE issue_id = $1 ORDER BY created_at, user_id`, id)
	if err != nil {
		return err
	}
	for arows.Next() {
		var u uuid.UUID
		if err := arows.Scan(&u); err != nil {
			arows.Close()
			return err
		}
		assigneeIDs = append(assigneeIDs, u.String())
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return err
	}
	ids := append([]string{authorID.String()}, assigneeIDs...)
	if p.Assignee != nil {
		ids = append(ids, p.Assignee.ID)
	}
	if err := b.resolve(ctx, ids...); err != nil {
		return err
	}
	assignees := make([]any, 0, len(assigneeIDs))
	for _, a := range assigneeIDs {
		assignees = append(assignees, b.user(a))
	}
	var milestone any
	if milestoneID != nil && msNumber != nil && msTitle != nil {
		milestone = obj{"id": milestoneID.String(), "number": *msNumber, "title": *msTitle}
	}
	issue := obj{
		"id": id.String(), "number": number, "title": title, "body": body, "state": state,
		"author": b.user(authorID.String()), "assignees": assignees, "labels": labels, "milestone": milestone,
		"locked": locked, "createdAt": createdAt.UTC().Format(time.RFC3339), "updatedAt": updatedAt.UTC().Format(time.RFC3339),
	}
	if closeReason != nil {
		issue["closeReason"] = *closeReason
	}
	if duplicateOf != nil {
		issue["duplicateOf"] = *duplicateOf
	}
	if closedAt != nil {
		issue["closedAt"] = closedAt.UTC().Format(time.RFC3339)
	}
	b.body["issue"] = issue
	if p.Assignee != nil {
		a, err := b.userFromEvent(ctx, p.Assignee)
		if err != nil {
			return err
		}
		b.body["assignee"] = a
	}
	if p.Label != nil {
		b.body["label"] = labelRow{ID: p.Label.ID, Name: p.Label.Name, Color: p.Label.Color}
	}
	if p.Milestone != nil {
		b.body["milestone"] = obj{"id": p.Milestone.ID, "number": p.Milestone.Number, "title": p.Milestone.Title}
	}
	if p.Changes != nil {
		ch := obj{}
		if p.Changes.Title != nil {
			ch["title"] = obj{"from": p.Changes.Title.From}
		}
		if p.Changes.Body != nil {
			ch["body"] = obj{"from": p.Changes.Body.From}
		}
		if len(ch) > 0 {
			b.body["changes"] = ch
		}
	}
	if p.Commit != nil {
		b.body["commit"] = obj{"sha": p.Commit.SHA, "repository": p.Commit.Repository}
	}
	return nil
}

// commentPayload: il commento letto dal database; per `deleted` il corpo è
// vuoto (I4).
func (b *builder) commentPayload(ctx context.Context, tx pgx.Tx, p domainevents.IssueCommentPayload) error {
	cid, err := uuid.Parse(p.Comment.ID)
	if err != nil {
		return &badEventError{err}
	}
	var body string
	var authorID uuid.UUID
	var createdAt, updatedAt time.Time
	var deletedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT body, author_id, created_at, updated_at, deleted_at FROM core.issue_comments WHERE id = $1`, cid).
		Scan(&body, &authorID, &createdAt, &updatedAt, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &badEventError{errors.New("il commento non esiste più")}
	}
	if err != nil {
		return err
	}
	if deletedAt != nil {
		body = "" // I4: un commento eliminato non ha più il testo
	}
	if err := b.resolve(ctx, authorID.String()); err != nil {
		return err
	}
	b.body["issue"] = obj{"id": p.Issue.ID, "number": p.Issue.Number, "title": p.Issue.Title, "state": p.Issue.State, "locked": p.Issue.Locked}
	b.body["comment"] = obj{
		"id": cid.String(), "body": body, "author": b.user(authorID.String()),
		"createdAt": createdAt.UTC().Format(time.RFC3339), "updatedAt": updatedAt.UTC().Format(time.RFC3339),
	}
	if p.Changes != nil && p.Changes.Body != nil {
		b.body["changes"] = obj{"body": obj{"from": p.Changes.Body.From}}
	}
	return nil
}
