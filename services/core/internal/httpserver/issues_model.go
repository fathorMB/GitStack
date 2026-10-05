package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Modello delle issues (M-05/C, GIT-103): righe, lettura con etichette,
// assegnatari e milestone, utenti risolti da identity, eventi. Gli handler
// stanno in issues_core.go.

// querier è ciò che hanno in comune il pool e una transazione.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// issueRow è una riga di core.issues con il conteggio dei commenti.
type issueRow struct {
	ID           uuid.UUID
	Number       int64
	Title        string
	Body         string
	State        string
	CloseReason  *string
	DuplicateOf  *int64
	AuthorID     uuid.UUID
	MilestoneID  *uuid.UUID
	Locked       bool
	Hidden       bool
	Edited       bool
	ClosedAt     *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CommentCount int
	ViaTokenID   *uuid.UUID
	ViaTokenName *string
}

const issueCols = `i.id, i.number, i.title, i.body, i.state, i.close_reason, i.duplicate_of, i.author_id,
	i.milestone_id, i.locked, i.hidden, i.edited, i.closed_at, i.created_at, i.updated_at,
	(SELECT count(*) FROM core.issue_comments c WHERE c.issue_id = i.id AND c.deleted_at IS NULL),
	i.via_token_id, i.via_token_name`

func scanIssue(r pgx.Row) (issueRow, error) {
	var x issueRow
	err := r.Scan(&x.ID, &x.Number, &x.Title, &x.Body, &x.State, &x.CloseReason, &x.DuplicateOf, &x.AuthorID,
		&x.MilestoneID, &x.Locked, &x.Hidden, &x.Edited, &x.ClosedAt, &x.CreatedAt, &x.UpdatedAt, &x.CommentCount, &x.ViaTokenID, &x.ViaTokenName)
	return x, err
}

// getIssue legge una issue per numero; forUpdate la blocca nella transazione.
// store.ErrNotFound se non esiste.
func getIssue(ctx context.Context, q querier, repoID uuid.UUID, number int64, forUpdate bool) (issueRow, error) {
	sql := `SELECT ` + issueCols + ` FROM core.issues i WHERE i.repo_id = $1 AND i.number = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF i`
	}
	x, err := scanIssue(q.QueryRow(ctx, sql, repoID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		return issueRow{}, store.ErrNotFound
	}
	return x, err
}

// issueExtras sono i dati collegati a una pagina di issues.
type issueExtras struct {
	labels     map[uuid.UUID][]openapi.IssueLabelRef
	assignees  map[uuid.UUID][]uuid.UUID
	milestones map[uuid.UUID]openapi.IssueMilestoneRef
}

func loadExtras(ctx context.Context, q querier, issues []issueRow) (issueExtras, error) {
	ex := issueExtras{
		labels:     map[uuid.UUID][]openapi.IssueLabelRef{},
		assignees:  map[uuid.UUID][]uuid.UUID{},
		milestones: map[uuid.UUID]openapi.IssueMilestoneRef{},
	}
	if len(issues) == 0 {
		return ex, nil
	}
	ids := make([]uuid.UUID, len(issues))
	for i, x := range issues {
		ids[i] = x.ID
	}
	rows, err := q.Query(ctx, `SELECT il.issue_id, l.id, l.name, l.color FROM core.issue_labels il
		JOIN core.labels l ON l.id = il.label_id WHERE il.issue_id = ANY($1) ORDER BY lower(l.name)`, ids)
	if err != nil {
		return ex, err
	}
	for rows.Next() {
		var issueID, labelID uuid.UUID
		var name, color string
		if err := rows.Scan(&issueID, &labelID, &name, &color); err != nil {
			rows.Close()
			return ex, err
		}
		ex.labels[issueID] = append(ex.labels[issueID], openapi.IssueLabelRef{Id: openapi_types.UUID(labelID), Name: name, Color: color})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ex, err
	}

	rows, err = q.Query(ctx, `SELECT issue_id, user_id FROM core.issue_assignees
		WHERE issue_id = ANY($1) ORDER BY created_at, user_id`, ids)
	if err != nil {
		return ex, err
	}
	for rows.Next() {
		var issueID, userID uuid.UUID
		if err := rows.Scan(&issueID, &userID); err != nil {
			rows.Close()
			return ex, err
		}
		ex.assignees[issueID] = append(ex.assignees[issueID], userID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ex, err
	}

	rows, err = q.Query(ctx, `SELECT i.id, m.number, m.title, m.state FROM core.issues i
		JOIN core.milestones m ON m.id = i.milestone_id WHERE i.id = ANY($1)`, ids)
	if err != nil {
		return ex, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID uuid.UUID
		var m openapi.IssueMilestoneRef
		var state string
		if err := rows.Scan(&issueID, &m.Number, &m.Title, &state); err != nil {
			return ex, err
		}
		m.State = openapi.IssueMilestoneRefState(state)
		ex.milestones[issueID] = m
	}
	return ex, rows.Err()
}

// userDirectory risolve gli id utente in nome e tipo tramite identity.
type userDirectory map[uuid.UUID]openapi.IssueUser

// errUsersUnavailable: identity non risolve gli utenti (503).
var errUsersUnavailable = errors.New("identity non risolve gli utenti")

// resolveUsers chiede a identity gli utenti degli id dati. Un id che identity
// non conosce (utente eliminato) compare come `ghost`.
func (s *apiServer) resolveUsers(ctx context.Context, ids []uuid.UUID) (userDirectory, error) {
	dir := userDirectory{}
	seen := map[uuid.UUID]bool{}
	var uniq []uuid.UUID
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return dir, nil
	}
	look, ok := s.repoIdentity.(identityclient.UserLookup)
	if !ok {
		return nil, fmt.Errorf("%w: client senza LookupUsers", errUsersUnavailable)
	}
	found, err := look.LookupUsers(ctx, uniq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUsersUnavailable, err)
	}
	for _, id := range uniq {
		u, ok := found[id]
		if !ok {
			dir[id] = openapi.IssueUser{Id: openapi_types.UUID(id), Username: "ghost", Kind: openapi.IssueUserKindHuman}
			continue
		}
		dir[id] = openapi.IssueUser{Id: openapi_types.UUID(id), Username: u.Username, Kind: openapi.IssueUserKind(u.Kind)}
	}
	return dir, nil
}

// issueViews compone Issue e IssueSummary per un gruppo di righe.
type issueViews struct {
	rows   []issueRow
	extras issueExtras
	users  userDirectory
}

func (s *apiServer) loadViews(ctx context.Context, q querier, rows []issueRow) (issueViews, error) {
	ex, err := loadExtras(ctx, q, rows)
	if err != nil {
		return issueViews{}, err
	}
	var uids []uuid.UUID
	for _, x := range rows {
		uids = append(uids, x.AuthorID)
		uids = append(uids, ex.assignees[x.ID]...)
	}
	dir, err := s.resolveUsers(ctx, uids)
	if err != nil {
		return issueViews{}, err
	}
	return issueViews{rows: rows, extras: ex, users: dir}, nil
}

func (v issueViews) assignees(x issueRow) []openapi.IssueUser {
	out := make([]openapi.IssueUser, 0, len(v.extras.assignees[x.ID]))
	for _, id := range v.extras.assignees[x.ID] {
		out = append(out, v.users[id])
	}
	return out
}

func (v issueViews) labels(x issueRow) []openapi.IssueLabelRef {
	if l := v.extras.labels[x.ID]; l != nil {
		return l
	}
	return []openapi.IssueLabelRef{}
}

func (v issueViews) milestone(x issueRow) *openapi.IssueMilestoneRef {
	if m, ok := v.extras.milestones[x.ID]; ok {
		return &m
	}
	return nil
}

// viaToken compone il token di origine; nil per le sessioni web.
func viaToken(id *uuid.UUID, name *string) *openapi.IssueViaToken {
	if id == nil {
		return nil
	}
	n := ""
	if name != nil {
		n = *name
	}
	return &openapi.IssueViaToken{Id: openapi_types.UUID(*id), Name: n}
}

// tokenOrigin ritorna id e nome del token del chiamante da salvare alla
// creazione (nil, nil per le sessioni web).
func tokenOrigin(c trust.Identity) (*uuid.UUID, *string) {
	id, err := uuid.Parse(c.TokenID)
	if c.TokenID == "" || err != nil {
		return nil, nil
	}
	name := c.TokenName
	return &id, &name
}

func closeReasonPtr(r *string) *openapi.IssueCloseReason {
	if r == nil {
		return nil
	}
	c := openapi.IssueCloseReason(*r)
	return &c
}

func (v issueViews) issue(x issueRow) openapi.Issue {
	return openapi.Issue{
		Id:           openapi_types.UUID(x.ID),
		Number:       x.Number,
		Title:        x.Title,
		Body:         x.Body,
		State:        openapi.IssueState(x.State),
		CloseReason:  closeReasonPtr(x.CloseReason),
		DuplicateOf:  x.DuplicateOf,
		Author:       v.users[x.AuthorID],
		ViaToken:     viaToken(x.ViaTokenID, x.ViaTokenName),
		Labels:       v.labels(x),
		Assignees:    v.assignees(x),
		Milestone:    v.milestone(x),
		Locked:       x.Locked,
		Hidden:       x.Hidden,
		Edited:       x.Edited,
		CommentCount: x.CommentCount,
		ClosedAt:     x.ClosedAt,
		CreatedAt:    x.CreatedAt,
		UpdatedAt:    x.UpdatedAt,
	}
}

func (v issueViews) summary(x issueRow) openapi.IssueSummary {
	locked := x.Locked
	return openapi.IssueSummary{
		Number:       x.Number,
		Title:        x.Title,
		State:        openapi.IssueState(x.State),
		CloseReason:  closeReasonPtr(x.CloseReason),
		Author:       v.users[x.AuthorID],
		Labels:       v.labels(x),
		Assignees:    v.assignees(x),
		Milestone:    v.milestone(x),
		Locked:       &locked,
		CommentCount: x.CommentCount,
		CreatedAt:    x.CreatedAt,
		UpdatedAt:    x.UpdatedAt,
	}
}

// writeIssueFailure risponde a un errore di lettura o composizione.
func writeIssueFailure(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, errUsersUnavailable) {
		slog.Default().Warn("utenti delle issues non risolti", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile risolvere gli utenti.")
		return
	}
	slog.Default().Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno: "+what+".")
}

// insertIssueEvent scrive un evento nella cronologia (stessa transazione
// dell'azione).
func insertIssueEvent(ctx context.Context, q querier, issueID uuid.UUID, typ string, actor uuid.UUID, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO core.issue_events (id, issue_id, type, actor_id, data, created_at)
		VALUES ($1, $2, $3, $4, $5, clock_timestamp())`,
		uuid.New(), issueID, typ, actor, b)
	return err
}

// lockRepoForWrite blocca in lettura la riga del repo e verifica che non sia
// archiviato né eliminato: serializza le scritture sulle issues con
// l'archiviazione (R10). store.ErrArchived o store.ErrNotFound.
func lockRepoForWrite(ctx context.Context, q querier, repoID uuid.UUID) error {
	var archived, deleted *time.Time
	err := q.QueryRow(ctx, `SELECT archived_at, deleted_at FROM core.repositories WHERE resource_id = $1 FOR SHARE`, repoID).Scan(&archived, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && deleted != nil) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if archived != nil {
		return store.ErrArchived
	}
	return nil
}
