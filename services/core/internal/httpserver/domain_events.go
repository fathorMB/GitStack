package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Eventi di dominio di core (M-06/B, GIT-130; contratto: docs/events.md).
// Ogni funzione emit* scrive l'evento nell'outbox (internal/outbox) con la
// transazione q della modifica: se la transazione non arriva al commit non
// resta niente, se ci arriva il relay lo pubblica su NATS, anche dopo un
// riavvio o con NATS irraggiungibile. L'evento descrive lo stato DOPO la
// modifica, quindi si chiama dopo l'UPDATE/INSERT corrispondente.
//
// actor.type (human|agent) non lo conosce la richiesta: lo aggiunge il relay
// interrogando identity al momento dell'invio. «Via token» non è nel
// contratto (nota CTO su GIT-130): lo porterà GIT-125.

// actorOf è chi ha agito: l'id dato e, se coincide col chiamante della
// richiesta, il suo username (header firmato dal gateway).
func actorOf(ctx context.Context, id uuid.UUID) *domainevents.User {
	u := &domainevents.User{ID: id.String()}
	if caller, ok := trust.FromContext(ctx); ok && caller.UserID == id.String() {
		u.Username = caller.Username
	}
	return u
}

func repoSnapshot(r store.Repo) domainevents.Repo {
	return domainevents.Repo{
		ID: r.ID.String(), FullName: r.OwnerName + "/" + r.Name, DefaultBranch: r.DefaultBranch,
		Visibility: r.Visibility, Archived: r.ArchivedAt != nil,
	}
}

// issueSnapshot legge repo e issue come sono ora, nella transazione q.
func issueSnapshot(ctx context.Context, q querier, issueID uuid.UUID) (domainevents.Repo, domainevents.Issue, error) {
	var repo domainevents.Repo
	var iss domainevents.Issue
	var repoID, authorID, id uuid.UUID
	var owner, name string
	var archived bool
	err := q.QueryRow(ctx, `SELECT r.resource_id, r.owner_name, r.name, r.default_branch, r.visibility, r.archived_at IS NOT NULL,
			i.id, i.number, i.title, i.state, i.author_id, i.hidden, i.locked
		FROM core.issues i JOIN core.repositories r ON r.resource_id = i.repo_id WHERE i.id = $1`, issueID).
		Scan(&repoID, &owner, &name, &repo.DefaultBranch, &repo.Visibility, &archived,
			&id, &iss.Number, &iss.Title, &iss.State, &authorID, &iss.Hidden, &iss.Locked)
	if err != nil {
		return repo, iss, fmt.Errorf("istantanea della issue per l'evento: %w", err)
	}
	repo.ID, repo.FullName, repo.Archived = repoID.String(), owner+"/"+name, archived
	iss.ID, iss.AuthorID = id.String(), authorID.String()
	iss.AssigneeIDs = []string{}
	rows, err := q.Query(ctx, `SELECT user_id FROM core.issue_assignees WHERE issue_id = $1 ORDER BY created_at, user_id`, issueID)
	if err != nil {
		return repo, iss, fmt.Errorf("assegnatari per l'evento: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			return repo, iss, err
		}
		iss.AssigneeIDs = append(iss.AssigneeIDs, u.String())
	}
	return repo, iss, rows.Err()
}

// emitIssue accoda un evento issue.*; mod aggiunge i campi propri dell'evento.
func emitIssue(ctx context.Context, q querier, name string, issueID, actor uuid.UUID, mod func(*domainevents.IssuePayload)) error {
	repo, iss, err := issueSnapshot(ctx, q, issueID)
	if err != nil {
		return err
	}
	p := domainevents.IssuePayload{Repo: repo, Actor: actorOf(ctx, actor), Issue: iss}
	if mod != nil {
		mod(&p)
	}
	return outbox.Enqueue(ctx, q, name, domainevents.Version, p)
}

// emitComment accoda un evento issue_comment.*.
func emitComment(ctx context.Context, q querier, name string, issueID, actor uuid.UUID, c domainevents.Comment, changes *domainevents.CommentChanges) error {
	repo, iss, err := issueSnapshot(ctx, q, issueID)
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, q, name, domainevents.Version, domainevents.IssueCommentPayload{
		Repo: repo, Actor: actorOf(ctx, actor), Issue: iss, Comment: c, Changes: changes,
	})
}

// emitRepo accoda un evento repository.*.
func emitRepo(ctx context.Context, q outbox.Execer, name string, repo store.Repo, actor uuid.UUID, mod func(*domainevents.RepositoryPayload)) error {
	p := domainevents.RepositoryPayload{Repo: repoSnapshot(repo), Actor: actorOf(ctx, actor)}
	if mod != nil {
		mod(&p)
	}
	return outbox.Enqueue(ctx, q, name, domainevents.Version, p)
}

// issueEventNames collega i tipi della cronologia (core.issue_events) agli
// eventi di dominio che nascono da una riga sola. Gli altri tipi li emette
// l'handler con i dati che solo lui ha: opened (assegnatari iniziali),
// renamed/edited (un solo issue.edited), assigned/unassigned (id utente) e
// comment_deleted (issue_comment.deleted).
var issueEventNames = map[string]string{
	"closed": "issue.closed", "reopened": "issue.reopened",
	"labeled": "issue.labeled", "unlabeled": "issue.unlabeled",
	"milestoned": "issue.milestoned", "demilestoned": "issue.demilestoned",
	"locked": "issue.locked", "unlocked": "issue.unlocked",
	"hidden": "issue.hidden", "unhidden": "issue.unhidden",
}

// emitFromHistory è chiamata da insertIssueEvent, nella stessa transazione:
// la cronologia e l'evento di dominio nascono insieme.
func emitFromHistory(ctx context.Context, q querier, issueID uuid.UUID, typ string, actor uuid.UUID, data map[string]any) error {
	name, ok := issueEventNames[typ]
	if !ok {
		return nil
	}
	var mod func(*domainevents.IssuePayload)
	switch typ {
	case "closed":
		reason, _ := data["reason"].(string)
		var dup *int64
		if n, ok := data["duplicateOf"].(int64); ok {
			dup = &n
		}
		mod = func(p *domainevents.IssuePayload) { p.Reason, p.DuplicateOf = reason, dup }
	case "locked":
		reason, _ := data["reason"].(string)
		mod = func(p *domainevents.IssuePayload) { p.Reason = reason }
	case "labeled", "unlabeled":
		label, _ := data["label"].(string)
		l := &domainevents.Label{Name: label}
		err := q.QueryRow(ctx, `SELECT l.id::text, l.name, l.color FROM core.labels l JOIN core.issues i ON i.repo_id = l.repo_id
			WHERE i.id = $1 AND lower(l.name) = lower($2)`, issueID, label).Scan(&l.ID, &l.Name, &l.Color)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("etichetta per l'evento: %w", err)
		}
		mod = func(p *domainevents.IssuePayload) { p.Label = l }
	case "milestoned", "demilestoned":
		var ms struct {
			Number int64  `json:"number"`
			Title  string `json:"title"`
		}
		b, _ := json.Marshal(data["milestone"])
		_ = json.Unmarshal(b, &ms)
		m := &domainevents.Milestone{Number: ms.Number, Title: ms.Title}
		err := q.QueryRow(ctx, `SELECT m.id::text FROM core.milestones m JOIN core.issues i ON i.repo_id = m.repo_id
			WHERE i.id = $1 AND m.number = $2`, issueID, ms.Number).Scan(&m.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("milestone per l'evento: %w", err)
		}
		mod = func(p *domainevents.IssuePayload) { p.Milestone = m }
	}
	return emitIssue(ctx, q, name, issueID, actor, mod)
}

// emitRepoUpdate accoda gli eventi di una modifica del repo: archiviazione o
// riattivazione, cambio di visibilità (con quella precedente).
func (s *apiServer) emitRepoUpdate(ctx context.Context, q outbox.Execer, cur, updated store.Repo, actor uuid.UUID) error {
	if (cur.ArchivedAt == nil) != (updated.ArchivedAt == nil) {
		name := domainevents.RepositoryArchived
		if updated.ArchivedAt == nil {
			name = domainevents.RepositoryUnarchived
		}
		if err := emitRepo(ctx, q, name, updated, actor, nil); err != nil {
			return err
		}
	}
	if updated.Visibility != cur.Visibility {
		return emitRepo(ctx, q, domainevents.RepositoryVisibilityChanged, updated, actor, func(p *domainevents.RepositoryPayload) {
			p.Changes = &domainevents.RepositoryChanges{Visibility: &domainevents.From{From: cur.Visibility}}
		})
	}
	return nil
}
