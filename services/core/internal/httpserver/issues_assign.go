package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Assegnatari, etichette e milestone di una issue (M-05/E, GIT-105, I3, I5,
// I6, I7). Serve write; ogni aggiunta o rimozione è un evento nella
// cronologia, nella stessa transazione. Il filtro assignee:@agents è M-05/F.

const maxAssignees = 10

type assignee struct {
	id   uuid.UUID
	name string
}

// resolveAssignees traduce gli username in utenti con write sul repo (I6).
// msg non vuoto = errore di validazione (422 su `assignees`); err = identity
// non disponibile (503).
func (s *apiServer) resolveAssignees(ctx context.Context, repoID uuid.UUID, names []string) (out []assignee, msg string, err error) {
	seen := map[string]bool{}
	for _, n := range names {
		key := strings.ToLower(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		o, rerr := s.repoIdentity.ResolveOwner(ctx, key)
		if errors.Is(rerr, identityclient.ErrNotFound) || (rerr == nil && o.Type != "user") {
			return nil, fmt.Sprintf("assignees: %q non è un utente.", n), nil
		}
		if rerr != nil {
			return nil, "", rerr
		}
		ok, rerr := s.repoIdentity.HasRole(ctx, o.ID, repoID, "write")
		if rerr != nil {
			return nil, "", rerr
		}
		if !ok {
			return nil, fmt.Sprintf("assignees: %q non ha il permesso write sul repo.", n), nil
		}
		out = append(out, assignee{id: o.ID, name: o.Name})
	}
	if len(out) > maxAssignees {
		return nil, "assignees: al massimo 10.", nil
	}
	return out, "", nil
}

// replaceAssignees porta gli assegnatari della issue a want, con gli eventi.
func (s *apiServer) replaceAssignees(ctx context.Context, tx querier, issueID, actor uuid.UUID, want []assignee) error {
	rows, err := tx.Query(ctx, `SELECT user_id FROM core.issue_assignees WHERE issue_id = $1`, issueID)
	if err != nil {
		return err
	}
	have := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		have[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	wantIDs := map[uuid.UUID]bool{}
	for _, a := range want {
		wantIDs[a.id] = true
		if have[a.id] {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core.issue_assignees (issue_id, user_id, assigned_by) VALUES ($1, $2, $3)`, issueID, a.id, actor); err != nil {
			return err
		}
		if err := insertIssueEvent(ctx, tx, issueID, "assigned", actor, map[string]any{"assignee": a.name}); err != nil {
			return err
		}
		if err := emitIssue(ctx, tx, domainevents.IssueAssigned, issueID, actor, func(p *domainevents.IssuePayload) {
			p.Assignee = &domainevents.User{ID: a.id.String(), Username: a.name}
		}); err != nil {
			return err
		}
	}
	var removed []uuid.UUID
	for id := range have {
		if !wantIDs[id] {
			removed = append(removed, id)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	dir, err := s.resolveUsers(ctx, removed)
	if err != nil {
		return err
	}
	for _, id := range removed {
		if _, err := tx.Exec(ctx, `DELETE FROM core.issue_assignees WHERE issue_id = $1 AND user_id = $2`, issueID, id); err != nil {
			return err
		}
		if err := insertIssueEvent(ctx, tx, issueID, "unassigned", actor, map[string]any{"assignee": dir[id].Username}); err != nil {
			return err
		}
		if err := emitIssue(ctx, tx, domainevents.IssueUnassigned, issueID, actor, func(p *domainevents.IssuePayload) {
			p.Assignee = &domainevents.User{ID: id.String(), Username: dir[id].Username}
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *apiServer) writeAssigneeFailure(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, identityclient.ErrUnavailable) {
		slog.Default().Warn(what, "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare gli assegnatari.")
		return
	}
	writeIssueFailure(w, what, err)
}

// beginIssueUpdate apre la transazione di una modifica di etichette,
// assegnatari o milestone: permessi, repo bloccato, issue bloccata.
func (s *apiServer) beginIssueUpdate(w http.ResponseWriter, r *http.Request, owner, name string, number int64) (issueAccess, pgx.Tx, issueRow, bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return ia, nil, issueRow{}, false
	}
	if !ia.requireManage(w) {
		return ia, nil, issueRow{}, false
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return ia, nil, issueRow{}, false
	}
	if !s.lockForManage(w, ctx, tx, ia) {
		_ = tx.Rollback(ctx)
		return ia, nil, issueRow{}, false
	}
	x, ok := s.issueFor(w, ctx, tx, ia, number, true)
	if !ok {
		_ = tx.Rollback(ctx)
		return ia, nil, issueRow{}, false
	}
	return ia, tx, x, true
}

func (s *apiServer) finishIssueUpdate(w http.ResponseWriter, r *http.Request, tx pgx.Tx, ia issueAccess, x issueRow) {
	ctx := r.Context()
	if _, err := tx.Exec(ctx, `UPDATE core.issues SET updated_at = now() WHERE id = $1`, x.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della issue non riuscito", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit non riuscito", err)
		return
	}
	s.respondIssue(w, ctx, http.StatusOK, ia, x.Number, nil)
}

// SetIssueAssignees implementa PUT .../issues/{n}/assignees (I6).
func (s *apiServer) SetIssueAssignees(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	var in openapi.SetIssueAssigneesInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	ia, tx, x, ok := s.beginIssueUpdate(w, r, owner, name, number)
	if !ok {
		return
	}
	ctx := r.Context()
	defer func() { _ = tx.Rollback(ctx) }()
	if in.Assignees == nil {
		in.Assignees = []string{}
	}
	want, msg, err := s.resolveAssignees(ctx, ia.repo.ID, in.Assignees)
	if err != nil {
		s.writeAssigneeFailure(w, "verifica degli assegnatari non riuscita", err)
		return
	}
	if msg != "" {
		writeFieldError(w, "assignees", msg)
		return
	}
	if err := s.replaceAssignees(ctx, tx, x.ID, ia.userID, want); err != nil {
		s.writeAssigneeFailure(w, "modifica degli assegnatari non riuscita", err)
		return
	}
	s.finishIssueUpdate(w, r, tx, ia, x)
}

// SetIssueLabels implementa PUT .../issues/{n}/labels (I5): sostituisce
// l'elenco; un'etichetta inesistente è 422, non si crea al volo.
func (s *apiServer) SetIssueLabels(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	var in openapi.SetIssueLabelsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	ia, tx, x, ok := s.beginIssueUpdate(w, r, owner, name, number)
	if !ok {
		return
	}
	ctx := r.Context()
	defer func() { _ = tx.Rollback(ctx) }()
	want := map[uuid.UUID]string{}
	for _, ln := range in.Labels {
		var id uuid.UUID
		var real string
		err := tx.QueryRow(ctx, `SELECT id, name FROM core.labels WHERE repo_id = $1 AND lower(name) = lower($2)`, ia.repo.ID, ln).Scan(&id, &real)
		if errors.Is(err, pgx.ErrNoRows) {
			writeFieldError(w, "labels", fmt.Sprintf("labels: l'etichetta %q non esiste nel repo.", ln))
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura delle etichette non riuscita", err)
			return
		}
		want[id] = real
	}
	rows, err := tx.Query(ctx, `SELECT l.id, l.name FROM core.issue_labels il JOIN core.labels l ON l.id = il.label_id WHERE il.issue_id = $1 ORDER BY lower(l.name)`, x.ID)
	if err != nil {
		writeIssueFailure(w, "lettura delle etichette non riuscita", err)
		return
	}
	have := map[uuid.UUID]string{}
	var haveOrder []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle etichette non riuscita", err)
			return
		}
		have[id] = n
		haveOrder = append(haveOrder, id)
	}
	rows.Close()
	for _, id := range haveOrder {
		if _, keep := want[id]; keep {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM core.issue_labels WHERE issue_id = $1 AND label_id = $2`, x.ID, id); err != nil {
			writeIssueFailure(w, "rimozione dell'etichetta non riuscita", err)
			return
		}
		if err := insertIssueEvent(ctx, tx, x.ID, "unlabeled", ia.userID, map[string]any{"label": have[id]}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	for _, ln := range in.Labels {
		for id, real := range want {
			if !strings.EqualFold(real, ln) {
				continue
			}
			if _, had := have[id]; had {
				break
			}
			if _, err := tx.Exec(ctx, `INSERT INTO core.issue_labels (issue_id, label_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, x.ID, id); err != nil {
				writeIssueFailure(w, "assegnazione dell'etichetta non riuscita", err)
				return
			}
			if err := insertIssueEvent(ctx, tx, x.ID, "labeled", ia.userID, map[string]any{"label": real}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
			have[id] = real
			break
		}
	}
	s.finishIssueUpdate(w, r, tx, ia, x)
}

// SetIssueMilestone implementa PUT .../issues/{n}/milestone (I7): una sola
// per issue; null la toglie.
func (s *apiServer) SetIssueMilestone(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	var in openapi.SetIssueMilestoneInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	ia, tx, x, ok := s.beginIssueUpdate(w, r, owner, name, number)
	if !ok {
		return
	}
	ctx := r.Context()
	defer func() { _ = tx.Rollback(ctx) }()
	var oldNum *int64
	var oldTitle string
	if x.MilestoneID != nil {
		var n int64
		if err := tx.QueryRow(ctx, `SELECT number, title FROM core.milestones WHERE id = $1`, *x.MilestoneID).Scan(&n, &oldTitle); err != nil {
			writeIssueFailure(w, "lettura della milestone non riuscita", err)
			return
		}
		oldNum = &n
	}
	var newID *uuid.UUID
	var newTitle string
	if in.Milestone != nil {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id, title FROM core.milestones WHERE repo_id = $1 AND number = $2`, ia.repo.ID, *in.Milestone).Scan(&id, &newTitle)
		if errors.Is(err, pgx.ErrNoRows) {
			writeFieldError(w, "milestone", "milestone: non esiste nel repo.")
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura della milestone non riuscita", err)
			return
		}
		newID = &id
	}
	same := (newID == nil && oldNum == nil) || (newID != nil && x.MilestoneID != nil && *newID == *x.MilestoneID)
	if !same {
		if _, err := tx.Exec(ctx, `UPDATE core.issues SET milestone_id = $2 WHERE id = $1`, x.ID, newID); err != nil {
			writeIssueFailure(w, "modifica della milestone non riuscita", err)
			return
		}
		if oldNum != nil {
			if err := insertIssueEvent(ctx, tx, x.ID, "demilestoned", ia.userID, map[string]any{"milestone": map[string]any{"number": *oldNum, "title": oldTitle}}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
		if newID != nil {
			if err := insertIssueEvent(ctx, tx, x.ID, "milestoned", ia.userID, map[string]any{"milestone": map[string]any{"number": *in.Milestone, "title": newTitle}}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
	}
	s.finishIssueUpdate(w, r, tx, ia, x)
}
