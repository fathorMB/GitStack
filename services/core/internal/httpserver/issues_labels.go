package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Etichette del repo (M-05/E, GIT-105, I5): per repo, gestite da chi ha
// write, lette da chi vede il repo. Nessun automatismo: solo le predefinite
// alla creazione del repo (repos.go).

const (
	maxLabelNameRunes = 50
	maxLabelDescRunes = 256
)

var labelColorRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// writeFieldError risponde 422 con details.fields.<campo> (contratto).
func writeFieldError(w http.ResponseWriter, field, msg string) {
	var body openapi.Error
	body.Error.Code = "validation_failed"
	body.Error.Message = msg
	d := map[string]interface{}{"fields": map[string]interface{}{field: msg}}
	body.Error.Details = &d
	writeJSON(w, http.StatusUnprocessableEntity, body)
}

// requireManage: chi gestisce etichette, milestone e assegnatari ha write
// (I3); il repo archiviato è in sola lettura (R10). Ritorna false dopo aver
// risposto.
func (ia issueAccess) requireManage(w http.ResponseWriter) bool {
	if !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Serve il permesso write sul repo.")
		return false
	}
	return !ia.rejectArchived(w)
}

func labelNameError(name string) string {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxLabelNameRunes {
		return "name: da 1 a 50 caratteri."
	}
	if strings.ContainsAny(name, "/,") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "name: non può contenere '/', virgole o caratteri di controllo."
	}
	return ""
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

const labelSelect = `SELECT l.id, l.name, l.color, l.description,
	(SELECT count(*) FROM core.issue_labels il JOIN core.issues i ON i.id = il.issue_id
	  WHERE il.label_id = l.id AND i.state = 'open' AND NOT i.hidden)
	FROM core.labels l`

func scanLabel(r pgx.Row) (openapi.Label, error) {
	var l openapi.Label
	var id uuid.UUID
	err := r.Scan(&id, &l.Name, &l.Color, &l.Description, &l.OpenIssues)
	l.Id = id
	return l, err
}

// ListLabels implementa GET /repos/{owner}/{repo}/labels.
func (s *apiServer) ListLabels(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListLabelsParams) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	page, perPage, ok := pageArgs(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(r.Context(), `SELECT count(*) FROM core.labels WHERE repo_id = $1`, ia.repo.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle etichette non riuscito", err)
		return
	}
	rows, err := s.pool.Query(r.Context(), labelSelect+` WHERE l.repo_id = $1 ORDER BY lower(l.name) LIMIT $2 OFFSET $3`,
		ia.repo.ID, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura delle etichette non riuscita", err)
		return
	}
	defer rows.Close()
	items := []openapi.Label{}
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			writeIssueFailure(w, "lettura delle etichette non riuscita", err)
			return
		}
		items = append(items, l)
	}
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle etichette non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.LabelList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// GetLabel implementa GET /repos/{owner}/{repo}/labels/{name}.
func (s *apiServer) GetLabel(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, label openapi.LabelNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	l, err := scanLabel(s.pool.QueryRow(r.Context(), labelSelect+` WHERE l.repo_id = $1 AND lower(l.name) = lower($2)`, ia.repo.ID, label))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Etichetta non trovata.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura dell'etichetta non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// pageArgs legge page e perPage con i default e i limiti di GET /repos.
func pageArgs(w http.ResponseWriter, p *openapi.PageParam, pp *openapi.PerPageParam) (int, int, bool) {
	page, perPage := defaultPage, defaultPerPage
	if p != nil {
		page = *p
	}
	if pp != nil {
		perPage = *pp
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return 0, 0, false
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return 0, 0, false
	}
	return page, perPage, true
}

// CreateLabel implementa POST /repos/{owner}/{repo}/labels.
func (s *apiServer) CreateLabel(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || !ia.requireManage(w) {
		return
	}
	var in openapi.CreateLabelInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if msg := labelNameError(in.Name); msg != "" {
		writeFieldError(w, "name", msg)
		return
	}
	if !labelColorRe.MatchString(in.Color) {
		writeFieldError(w, "color", "color: esadecimale a sei cifre, senza #.")
		return
	}
	desc := ""
	if in.Description != nil {
		desc = *in.Description
	}
	if utf8.RuneCountInString(desc) > maxLabelDescRunes {
		writeFieldError(w, "description", "description: al massimo 256 caratteri.")
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
	_, err = tx.Exec(ctx, `INSERT INTO core.labels (id, repo_id, name, color, description) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), ia.repo.ID, in.Name, strings.ToLower(in.Color), desc)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already_exists", "Esiste già un'etichetta con questo nome.")
		return
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeIssueFailure(w, "creazione dell'etichetta non riuscita", err)
		return
	}
	l, err := scanLabel(s.pool.QueryRow(ctx, labelSelect+` WHERE l.repo_id = $1 AND lower(l.name) = lower($2)`, ia.repo.ID, in.Name))
	if err != nil {
		writeIssueFailure(w, "lettura dell'etichetta non riuscita", err)
		return
	}
	w.Header().Set("Location", "/repos/"+ia.repo.OwnerName+"/"+ia.repo.Name+"/labels/"+url.PathEscape(l.Name))
	writeJSON(w, http.StatusCreated, l)
}

// lockForManage blocca il repo (non archiviato, non eliminato) per la
// transazione di una scrittura di etichette, milestone o assegnatari.
func (s *apiServer) lockForManage(w http.ResponseWriter, ctx context.Context, q querier, ia issueAccess) bool {
	err := lockRepoForWrite(ctx, q, ia.repo.ID)
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrArchived):
		writeArchived(w)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
	default:
		writeIssueFailure(w, "verifica del repo non riuscita", err)
	}
	return false
}

// UpdateLabel implementa PATCH /repos/{owner}/{repo}/labels/{name}.
func (s *apiServer) UpdateLabel(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, label openapi.LabelNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || !ia.requireManage(w) {
		return
	}
	var in openapi.UpdateLabelInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Name == nil && in.Color == nil && in.Description == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Nessun campo da modificare.")
		return
	}
	if in.Name != nil {
		if msg := labelNameError(*in.Name); msg != "" {
			writeFieldError(w, "name", msg)
			return
		}
	}
	if in.Color != nil && !labelColorRe.MatchString(*in.Color) {
		writeFieldError(w, "color", "color: esadecimale a sei cifre, senza #.")
		return
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > maxLabelDescRunes {
		writeFieldError(w, "description", "description: al massimo 256 caratteri.")
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
	err = tx.QueryRow(ctx, `SELECT id FROM core.labels WHERE repo_id = $1 AND lower(name) = lower($2) FOR UPDATE`, ia.repo.ID, label).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Etichetta non trovata.")
		return
	}
	if err == nil {
		var color *string
		if in.Color != nil {
			c := strings.ToLower(*in.Color)
			color = &c
		}
		_, err = tx.Exec(ctx, `UPDATE core.labels SET name = COALESCE($2, name), color = COALESCE($3, color),
			description = COALESCE($4, description) WHERE id = $1`, id, in.Name, color, in.Description)
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already_exists", "Esiste già un'etichetta con questo nome.")
		return
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeIssueFailure(w, "modifica dell'etichetta non riuscita", err)
		return
	}
	l, err := scanLabel(s.pool.QueryRow(ctx, labelSelect+` WHERE l.id = $1`, id))
	if err != nil {
		writeIssueFailure(w, "lettura dell'etichetta non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// DeleteLabel implementa DELETE /repos/{owner}/{repo}/labels/{name}: la
// toglie da tutte le issues, con un evento unlabeled ciascuna.
func (s *apiServer) DeleteLabel(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, label openapi.LabelNameParam) {
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
	var real string
	err = tx.QueryRow(ctx, `SELECT id, name FROM core.labels WHERE repo_id = $1 AND lower(name) = lower($2) FOR UPDATE`, ia.repo.ID, label).Scan(&id, &real)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Etichetta non trovata.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura dell'etichetta non riuscita", err)
		return
	}
	rows, err := tx.Query(ctx, `SELECT issue_id FROM core.issue_labels WHERE label_id = $1 ORDER BY issue_id`, id)
	if err != nil {
		writeIssueFailure(w, "lettura delle issues dell'etichetta non riuscita", err)
		return
	}
	var issueIDs []uuid.UUID
	for rows.Next() {
		var iid uuid.UUID
		if err := rows.Scan(&iid); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle issues dell'etichetta non riuscita", err)
			return
		}
		issueIDs = append(issueIDs, iid)
	}
	rows.Close()
	for _, iid := range issueIDs {
		if err := insertIssueEvent(ctx, tx, iid, "unlabeled", ia.userID, map[string]any{"label": real}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.labels WHERE id = $1`, id); err != nil {
		writeIssueFailure(w, "eliminazione dell'etichetta non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "eliminazione dell'etichetta non riuscita", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
