package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Milestone del repo (M-05/E, GIT-105, I7): numero per repo con il proprio
// contatore, titolo unico senza maiuscole, gestite da chi ha write.
// Avanzamento: aperte e chiuse come completed; not_planned e duplicate (I2)
// non contano. Le issues nascoste non entrano nei conteggi.

const (
	maxMilestoneTitleRunes = 256
	maxMilestoneDescRunes  = 4096
)

const milestoneSelect = `SELECT m.id, m.number, m.title, m.description, m.due_on, m.state, m.closed_at, m.created_at, m.updated_at,
	(SELECT count(*) FROM core.issues i WHERE i.milestone_id = m.id AND i.state = 'open' AND NOT i.hidden),
	(SELECT count(*) FROM core.issues i WHERE i.milestone_id = m.id AND i.state = 'closed' AND i.close_reason = 'completed' AND NOT i.hidden)
	FROM core.milestones m`

func scanMilestone(r pgx.Row) (uuid.UUID, openapi.Milestone, error) {
	var id uuid.UUID
	var m openapi.Milestone
	var due *time.Time
	var state string
	err := r.Scan(&id, &m.Number, &m.Title, &m.Description, &due, &state, &m.ClosedAt, &m.CreatedAt, &m.UpdatedAt, &m.OpenIssues, &m.ClosedIssues)
	m.State = openapi.MilestoneState(state)
	if due != nil {
		m.DueOn = &openapi_types.Date{Time: *due}
	}
	return id, m, err
}

func milestoneTitleError(t string) string {
	if n := utf8.RuneCountInString(t); n < 1 || n > maxMilestoneTitleRunes {
		return "title: da 1 a 256 caratteri."
	}
	return ""
}

// ListMilestones implementa GET /repos/{owner}/{repo}/milestones.
func (s *apiServer) ListMilestones(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListMilestonesParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	page, perPage, ok := pageArgs(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	state := "open"
	if params.State != nil {
		state = string(*params.State)
	}
	if state != "open" && state != "closed" && state != "all" {
		writeError(w, http.StatusBadRequest, "invalid_state", "state: open, closed o all.")
		return
	}
	const where = ` WHERE m.repo_id = $1 AND ($2 = 'all' OR m.state = $2)`
	var total int
	if err := s.pool.QueryRow(r.Context(), `SELECT count(*) FROM core.milestones m`+where, ia.repo.ID, state).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle milestone non riuscito", err)
		return
	}
	rows, err := s.pool.Query(r.Context(), milestoneSelect+where+` ORDER BY m.due_on NULLS LAST, m.number LIMIT $3 OFFSET $4`,
		ia.repo.ID, state, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura delle milestone non riuscita", err)
		return
	}
	defer rows.Close()
	items := []openapi.Milestone{}
	for rows.Next() {
		_, m, err := scanMilestone(rows)
		if err != nil {
			writeIssueFailure(w, "lettura delle milestone non riuscita", err)
			return
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle milestone non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.MilestoneList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// GetMilestone implementa GET /repos/{owner}/{repo}/milestones/{n}.
func (s *apiServer) GetMilestone(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.MilestoneNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	_, m, err := scanMilestone(s.pool.QueryRow(r.Context(), milestoneSelect+` WHERE m.repo_id = $1 AND m.number = $2`, ia.repo.ID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Milestone non trovata.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura della milestone non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// CreateMilestone implementa POST /repos/{owner}/{repo}/milestones.
func (s *apiServer) CreateMilestone(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || !ia.requireManage(w) {
		return
	}
	var in openapi.CreateMilestoneInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	title := strings.TrimSpace(in.Title)
	if msg := milestoneTitleError(title); msg != "" {
		writeFieldError(w, "title", msg)
		return
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	if utf8.RuneCountInString(desc) > maxMilestoneDescRunes {
		writeFieldError(w, "description", "description: al massimo 4096 caratteri.")
		return
	}
	var due *time.Time
	if in.DueOn != nil {
		due = &in.DueOn.Time
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !s.lockForManage(w, ctx, tx, ia) {
		return
	}
	var number int64
	err = tx.QueryRow(ctx, `UPDATE core.repo_counters SET next_milestone_number = next_milestone_number + 1
		WHERE repo_id = $1 RETURNING next_milestone_number - 1`, ia.repo.ID).Scan(&number)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err = tx.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1) ON CONFLICT (repo_id) DO NOTHING`, ia.repo.ID); err == nil {
			err = tx.QueryRow(ctx, `UPDATE core.repo_counters SET next_milestone_number = next_milestone_number + 1
				WHERE repo_id = $1 RETURNING next_milestone_number - 1`, ia.repo.ID).Scan(&number)
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO core.milestones (id, repo_id, number, title, description, due_on) VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), ia.repo.ID, number, title, desc, due)
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already_exists", "Esiste già una milestone con questo titolo.")
		return
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeIssueFailure(w, "creazione della milestone non riuscita", err)
		return
	}
	_, m, err := scanMilestone(s.pool.QueryRow(ctx, milestoneSelect+` WHERE m.repo_id = $1 AND m.number = $2`, ia.repo.ID, number))
	if err != nil {
		writeIssueFailure(w, "lettura della milestone non riuscita", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/repos/%s/%s/milestones/%d", ia.repo.OwnerName, ia.repo.Name, number))
	writeJSON(w, http.StatusCreated, m)
}

// UpdateMilestone implementa PATCH /repos/{owner}/{repo}/milestones/{n}.
// `dueOn: null` toglie la data: si distingue dall'assenza leggendo le chiavi.
func (s *apiServer) UpdateMilestone(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.MilestoneNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || !ia.requireManage(w) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIssueRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	var in openapi.UpdateMilestoneInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var keys map[string]json.RawMessage
	if dec.Decode(&in) != nil || json.Unmarshal(raw, &keys) != nil || len(keys) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	_, setDue := keys["dueOn"]
	var title *string
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if msg := milestoneTitleError(t); msg != "" {
			writeFieldError(w, "title", msg)
			return
		}
		title = &t
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > maxMilestoneDescRunes {
		writeFieldError(w, "description", "description: al massimo 4096 caratteri.")
		return
	}
	var state *string
	if in.State != nil {
		st := string(*in.State)
		if st != "open" && st != "closed" {
			writeFieldError(w, "state", "state: open o closed.")
			return
		}
		state = &st
	}
	var due *time.Time
	if in.DueOn != nil {
		due = &in.DueOn.Time
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !s.lockForManage(w, ctx, tx, ia) {
		return
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM core.milestones WHERE repo_id = $1 AND number = $2 FOR UPDATE`, ia.repo.ID, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Milestone non trovata.")
		return
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE core.milestones SET
			title = COALESCE($2, title),
			description = COALESCE($3, description),
			due_on = CASE WHEN $4 THEN $5::date ELSE due_on END,
			state = COALESCE($6, state),
			closed_at = CASE WHEN COALESCE($6, state) = 'closed' THEN COALESCE(closed_at, now()) ELSE NULL END,
			updated_at = now()
			WHERE id = $1`, id, title, in.Description, setDue, due, state)
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already_exists", "Esiste già una milestone con questo titolo.")
		return
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeIssueFailure(w, "modifica della milestone non riuscita", err)
		return
	}
	_, m, err := scanMilestone(s.pool.QueryRow(ctx, milestoneSelect+` WHERE m.id = $1`, id))
	if err != nil {
		writeIssueFailure(w, "lettura della milestone non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// DeleteMilestone implementa DELETE: le issues collegate restano, senza
// milestone, con un evento demilestoned ciascuna.
func (s *apiServer) DeleteMilestone(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.MilestoneNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || !ia.requireManage(w) {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !s.lockForManage(w, ctx, tx, ia) {
		return
	}
	var id uuid.UUID
	var title string
	err = tx.QueryRow(ctx, `SELECT id, title FROM core.milestones WHERE repo_id = $1 AND number = $2 FOR UPDATE`, ia.repo.ID, number).Scan(&id, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Milestone non trovata.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura della milestone non riuscita", err)
		return
	}
	rows, err := tx.Query(ctx, `SELECT id FROM core.issues WHERE milestone_id = $1 ORDER BY number`, id)
	if err != nil {
		writeIssueFailure(w, "lettura delle issues della milestone non riuscita", err)
		return
	}
	var issueIDs []uuid.UUID
	for rows.Next() {
		var iid uuid.UUID
		if err := rows.Scan(&iid); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle issues della milestone non riuscita", err)
			return
		}
		issueIDs = append(issueIDs, iid)
	}
	rows.Close()
	for _, iid := range issueIDs {
		if err := insertIssueEvent(ctx, tx, iid, "demilestoned", ia.userID, map[string]any{"milestone": map[string]any{"number": number, "title": title}}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	// ON DELETE SET NULL sulla FK toglie la milestone dalle issues.
	if _, err := tx.Exec(ctx, `DELETE FROM core.milestones WHERE id = $1`, id); err != nil {
		writeIssueFailure(w, "eliminazione della milestone non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "eliminazione della milestone non riuscita", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
