package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Operazioni del nucleo delle issues (M-05/C, GIT-103): creazione con il
// numero dal contatore del repo (I1), lettura, elenco base, modifica (I4),
// chiusura con motivo e riapertura (I2), nascondere (I4), versioni e
// cronologia. Permessi I3; repo archiviato in sola lettura (R10).
// Regole: .prisma/knowledge/topics/issues.md; contratto in api/openapi.yaml.

const (
	maxIssueTitleRunes = 256
	maxIssueBodyRunes  = 65536
	// maxIssueRequestBytes è il limite del corpo della richiesta: oltre, 413
	// body_too_large.
	maxIssueRequestBytes = 1 << 20
)

// issueAccess è il chiamante di fronte a un repo: ruoli effettivi.
type issueAccess struct {
	repo   store.Repo
	userID uuid.UUID
	caller trust.Identity
	write  bool
	admin  bool
}

// issueAccessFor risolve repo e ruoli del chiamante. Un repo inesistente o
// non leggibile è 404. Ritorna ok=false dopo aver già risposto.
func (s *apiServer) issueAccessFor(w http.ResponseWriter, r *http.Request, owner, name string) (issueAccess, bool) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return issueAccess{}, false
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sulle issues.")
		return issueAccess{}, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return issueAccess{}, false
	}
	ia := issueAccess{repo: repo, userID: userID, caller: caller}
	var err error
	if ia.admin, err = s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "admin"); err == nil {
		ia.write = ia.admin
		if !ia.write {
			ia.write, err = s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "write")
		}
	}
	if err != nil {
		slog.Default().Warn("verifica dei permessi sulle issues non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return issueAccess{}, false
	}
	return ia, true
}

// rejectArchived risponde 409 se il repo è archiviato (R10).
func (ia issueAccess) rejectArchived(w http.ResponseWriter) bool {
	if ia.repo.ArchivedAt != nil {
		writeArchived(w)
		return true
	}
	return false
}

func writeArchived(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: le issues sono in sola lettura.")
}

func writeIssueNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Issue non trovata.")
}

// decodeIssueJSON legge il corpo JSON (campi sconosciuti vietati). optional:
// un corpo vuoto è un oggetto vuoto.
func decodeIssueJSON(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxIssueRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil || (optional && errors.Is(err, io.EOF)) {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "La richiesta supera 1 MiB.")
		return false
	}
	writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
	return false
}

// issueFor trova la issue; una nascosta è 404 per chi non ha admin (I4).
func (s *apiServer) issueFor(w http.ResponseWriter, ctx context.Context, q querier, ia issueAccess, number int64, forUpdate bool) (issueRow, bool) {
	x, err := getIssue(ctx, q, ia.repo.ID, number, forUpdate)
	if errors.Is(err, store.ErrNotFound) || (err == nil && x.Hidden && !ia.admin) {
		writeIssueNotFound(w)
		return issueRow{}, false
	}
	if err != nil {
		writeIssueFailure(w, "lettura della issue non riuscita", err)
		return issueRow{}, false
	}
	return x, true
}

// respondIssue legge la issue (dopo il commit) e la scrive nella risposta.
func (s *apiServer) respondIssue(w http.ResponseWriter, ctx context.Context, status int, ia issueAccess, number int64, headers map[string]string) {
	x, err := getIssue(ctx, s.pool, ia.repo.ID, number, false)
	if err != nil {
		writeIssueFailure(w, "lettura della issue non riuscita", err)
		return
	}
	v, err := s.loadViews(ctx, s.pool, []issueRow{x})
	if err != nil {
		writeIssueFailure(w, "composizione della issue non riuscita", err)
		return
	}
	for k, val := range headers {
		w.Header().Set(k, val)
	}
	writeJSON(w, status, v.issue(x))
}

func issueTitleError(title string) string {
	n := utf8.RuneCountInString(title)
	if n < 1 || n > maxIssueTitleRunes {
		return "title: da 1 a 256 caratteri."
	}
	return ""
}

func issueBodyError(body string) string {
	if utf8.RuneCountInString(body) > maxIssueBodyRunes {
		return "body: al massimo 65536 caratteri."
	}
	return ""
}

// CreateIssue implementa POST /repos/{owner}/{repo}/issues (I1, I3).
func (s *apiServer) CreateIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok || ia.rejectArchived(w) {
		return
	}
	var in openapi.CreateIssueInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	title := strings.TrimSpace(in.Title)
	body := ""
	if in.Body != nil {
		body = *in.Body
	}
	if msg := issueTitleError(title); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
		return
	}
	if msg := issueBodyError(body); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
		return
	}
	var labelNames []string
	if in.Labels != nil {
		labelNames = *in.Labels
	}
	hasAssignees := in.Assignees != nil && len(*in.Assignees) > 0
	// I3: etichette, assegnatari e milestone sono di chi gestisce (write).
	if (len(labelNames) > 0 || hasAssignees || in.Milestone != nil) && !ia.write {
		writeError(w, http.StatusForbidden, "forbidden", "Etichette, assegnatari e milestone richiedono il permesso write.")
		return
	}
	var assignees []assignee
	if hasAssignees {
		var msg string
		var aerr error
		assignees, msg, aerr = s.resolveAssignees(r.Context(), ia.repo.ID, *in.Assignees)
		if aerr != nil {
			s.writeAssigneeFailure(w, "verifica degli assegnatari non riuscita", aerr)
			return
		}
		if msg != "" {
			writeFieldError(w, "assignees", msg)
			return
		}
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockRepoForWrite(ctx, tx, ia.repo.ID); err != nil {
		if errors.Is(err, store.ErrArchived) {
			writeArchived(w)
		} else if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		} else {
			writeIssueFailure(w, "verifica del repo non riuscita", err)
		}
		return
	}

	type labelRow struct {
		id   uuid.UUID
		name string
	}
	var labels []labelRow
	seen := map[string]bool{}
	for _, ln := range labelNames {
		key := strings.ToLower(ln)
		if seen[key] {
			continue
		}
		seen[key] = true
		var l labelRow
		err := tx.QueryRow(ctx, `SELECT id, name FROM core.labels WHERE repo_id = $1 AND lower(name) = lower($2)`, ia.repo.ID, ln).Scan(&l.id, &l.name)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "label_not_found", fmt.Sprintf("L'etichetta %q non esiste nel repo.", ln))
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura delle etichette non riuscita", err)
			return
		}
		labels = append(labels, l)
	}
	var milestoneID *uuid.UUID
	var milestoneNumber int64
	var milestoneTitle string
	if in.Milestone != nil {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id, number, title FROM core.milestones WHERE repo_id = $1 AND number = $2`, ia.repo.ID, *in.Milestone).Scan(&id, &milestoneNumber, &milestoneTitle)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "milestone_not_found", "La milestone non esiste nel repo.")
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura della milestone non riuscita", err)
			return
		}
		milestoneID = &id
	}

	// I1: il numero dal contatore del repo, nella stessa transazione
	// dell'INSERT. L'UPDATE serializza le creazioni concorrenti sulla riga del
	// contatore; se la transazione non arriva al commit il numero non è
	// stato assegnato a nessuno.
	number, err := nextRepoNumber(ctx, tx, ia.repo.ID)
	if err != nil {
		writeIssueFailure(w, "assegnazione del numero non riuscita", err)
		return
	}
	issueID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO core.issues (id, repo_id, number, title, body, author_id, milestone_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, issueID, ia.repo.ID, number, title, body, ia.userID, milestoneID); err != nil {
		writeIssueFailure(w, "creazione della issue non riuscita", err)
		return
	}
	if err := insertIssueEvent(ctx, tx, issueID, "opened", ia.userID, nil); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	for _, l := range labels {
		if _, err := tx.Exec(ctx, `INSERT INTO core.issue_labels (issue_id, label_id) VALUES ($1, $2)`, issueID, l.id); err != nil {
			writeIssueFailure(w, "assegnazione dell'etichetta non riuscita", err)
			return
		}
		if err := insertIssueEvent(ctx, tx, issueID, "labeled", ia.userID, map[string]any{"label": l.name}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	if err := s.replaceAssignees(ctx, tx, issueID, ia.userID, assignees); err != nil {
		s.writeAssigneeFailure(w, "assegnazione non riuscita", err)
		return
	}
	if milestoneID != nil {
		if err := insertIssueEvent(ctx, tx, issueID, "milestoned", ia.userID, map[string]any{"milestone": map[string]any{"number": milestoneNumber, "title": milestoneTitle}}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della issue non riuscito", err)
		return
	}
	loc := fmt.Sprintf("/repos/%s/%s/issues/%d", ia.repo.OwnerName, ia.repo.Name, number)
	s.respondIssue(w, ctx, http.StatusCreated, ia, number, map[string]string{"Location": loc})
}

// nextRepoNumber prende il prossimo #n del repo (I1). Un repo nato prima
// della migrazione 0004 potrebbe non avere la riga del contatore.
func nextRepoNumber(ctx context.Context, q querier, repoID uuid.UUID) (int64, error) {
	const sql = `UPDATE core.repo_counters SET next_number = next_number + 1
		WHERE repo_id = $1 RETURNING next_number - 1`
	var n int64
	err := q.QueryRow(ctx, sql, repoID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := q.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1) ON CONFLICT (repo_id) DO NOTHING`, repoID); err != nil {
			return 0, err
		}
		err = q.QueryRow(ctx, sql, repoID).Scan(&n)
	}
	return n, err
}

// GetIssue implementa GET /repos/{owner}/{repo}/issues/{number}.
func (s *apiServer) GetIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	x, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok {
		return
	}
	v, err := s.loadViews(r.Context(), s.pool, []issueRow{x})
	if err != nil {
		writeIssueFailure(w, "composizione della issue non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, v.issue(x))
}

// ListIssues implementa GET /repos/{owner}/{repo}/issues con i filtri
// semplici (stato, motivo, etichette); la ricerca completa (q, assegnatario,
// autore, milestone) è M-05/F.
func (s *apiServer) ListIssues(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, params openapi.ListIssuesParams) {
	page, perPage := defaultPage, defaultPerPage
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return
	}
	state := "open"
	if params.State != nil {
		state = string(*params.State)
		if state != "open" && state != "closed" && state != "all" {
			writeError(w, http.StatusBadRequest, "invalid_state", "state: open, closed o all.")
			return
		}
	}
	if params.Reason != nil && !params.Reason.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_reason", "reason: completed, not_planned o duplicate.")
		return
	}
	order := "i.number DESC"
	if params.Sort != nil {
		switch string(*params.Sort) {
		case "created":
		case "updated":
			order = "i.updated_at DESC, i.number DESC"
		case "comments":
			// 16 = il conteggio dei commenti, ultima colonna di issueCols.
			order = "16 DESC, i.number DESC"
		default:
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "sort=relevance richiede testo libero in q (M-05/F).")
			return
		}
	}
	for _, p := range []*string{params.Q, params.Assignee, params.Author, params.Milestone} {
		if p != nil && *p != "" {
			writeError(w, http.StatusNotImplemented, "not_implemented", "La ricerca per testo, assegnatario, autore e milestone non è ancora disponibile.")
			return
		}
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}

	where := []string{"i.repo_id = $1"}
	args := []any{ia.repo.ID}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if !ia.admin {
		where = append(where, "NOT i.hidden")
	}
	if state != "all" {
		where = append(where, "i.state = "+arg(state))
	}
	if params.Reason != nil {
		where = append(where, "i.close_reason = "+arg(string(*params.Reason)))
	}
	if params.Labels != nil {
		for _, ln := range strings.Split(*params.Labels, ",") {
			if ln = strings.TrimSpace(ln); ln == "" {
				continue
			}
			where = append(where, `EXISTS (SELECT 1 FROM core.issue_labels il JOIN core.labels l ON l.id = il.label_id
				WHERE il.issue_id = i.id AND lower(l.name) = lower(`+arg(ln)+`))`)
		}
	}
	cond := strings.Join(where, " AND ")

	ctx := r.Context()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.issues i WHERE `+cond, args...).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle issues non riuscito", err)
		return
	}
	limit, offset := arg(perPage), arg((page-1)*perPage)
	rows, err := s.pool.Query(ctx, `SELECT `+issueCols+` FROM core.issues i WHERE `+cond+
		` ORDER BY `+order+` LIMIT `+limit+` OFFSET `+offset, args...)
	if err != nil {
		writeIssueFailure(w, "elenco delle issues non riuscito", err)
		return
	}
	var list []issueRow
	for rows.Next() {
		x, err := scanIssue(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle issues non riuscita", err)
			return
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "elenco delle issues non riuscito", err)
		return
	}
	v, err := s.loadViews(ctx, s.pool, list)
	if err != nil {
		writeIssueFailure(w, "composizione delle issues non riuscita", err)
		return
	}
	items := make([]openapi.IssueSummary, 0, len(list))
	for _, x := range list {
		items = append(items, v.summary(x))
	}
	writeJSON(w, http.StatusOK, openapi.IssueList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// beginIssueWrite apre la transazione di una modifica: blocca il repo (R10)
// e la issue. Ritorna ok=false dopo aver già risposto; in tal caso la
// transazione è già chiusa.
func (s *apiServer) beginIssueWrite(w http.ResponseWriter, r *http.Request, ia issueAccess, number int64) (pgx.Tx, issueRow, bool) {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeIssueFailure(w, "apertura della transazione non riuscita", err)
		return nil, issueRow{}, false
	}
	if err := lockRepoForWrite(ctx, tx, ia.repo.ID); err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, store.ErrArchived) {
			writeArchived(w)
		} else if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		} else {
			writeIssueFailure(w, "verifica del repo non riuscita", err)
		}
		return nil, issueRow{}, false
	}
	x, ok := s.issueFor(w, ctx, tx, ia, number, true)
	if !ok {
		_ = tx.Rollback(ctx)
		return nil, issueRow{}, false
	}
	return tx, x, true
}

// UpdateIssue implementa PATCH /repos/{owner}/{repo}/issues/{number}: solo
// l'autore modifica il proprio testo (I4); la versione sostituita si
// conserva.
func (s *apiServer) UpdateIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	// Una issue nascosta non si rivela a chi non è admin, nemmeno con 409.
	cur, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok || ia.rejectArchived(w) {
		return
	}
	if cur.AuthorID != ia.userID {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'autore modifica il testo della issue.")
		return
	}
	var in openapi.UpdateIssueInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Title == nil && in.Body == nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "Serve almeno title o body.")
		return
	}
	var newTitle string
	if in.Title != nil {
		newTitle = strings.TrimSpace(*in.Title)
		if msg := issueTitleError(newTitle); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
			return
		}
	}
	if in.Body != nil {
		if msg := issueBodyError(*in.Body); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
			return
		}
	}

	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// I11: con la discussione bloccata modifica solo chi ha write.
	if x.Locked && !ia.write {
		writeError(w, http.StatusForbidden, "locked", "La discussione è bloccata.")
		return
	}
	titleChanged := in.Title != nil && newTitle != x.Title
	bodyChanged := in.Body != nil && *in.Body != x.Body
	if titleChanged || bodyChanged {
		title, body := x.Title, x.Body
		if titleChanged {
			title = newTitle
		}
		if bodyChanged {
			body = *in.Body
		}
		// La versione sostituita: 1 è il testo originale.
		if _, err := tx.Exec(ctx, `INSERT INTO core.issue_text_versions (id, issue_id, version, title, body, editor_id)
			VALUES ($1, $2, (SELECT COALESCE(max(version), 0) + 1 FROM core.issue_text_versions WHERE issue_id = $2 AND comment_id IS NULL), $3, $4, $5)`,
			uuid.New(), x.ID, x.Title, x.Body, ia.userID); err != nil {
			writeIssueFailure(w, "salvataggio della versione non riuscito", err)
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE core.issues SET title = $2, body = $3, edited = true, updated_at = now() WHERE id = $1`,
			x.ID, title, body); err != nil {
			writeIssueFailure(w, "modifica della issue non riuscita", err)
			return
		}
		if titleChanged {
			if err := insertIssueEvent(ctx, tx, x.ID, "renamed", ia.userID, map[string]any{"from": x.Title, "to": newTitle}); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
		if bodyChanged {
			if err := insertIssueEvent(ctx, tx, x.ID, "edited", ia.userID, nil); err != nil {
				writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondIssue(w, ctx, http.StatusOK, ia, number, nil)
}

// CloseIssue implementa POST .../close (I2, I3).
func (s *apiServer) CloseIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	cur, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok || ia.rejectArchived(w) {
		return
	}
	// I3: chi ha write chiude le issues altrui, l'autore la propria.
	if !ia.write && cur.AuthorID != ia.userID {
		writeError(w, http.StatusForbidden, "forbidden", "Chiude la issue chi ha write o l'autore.")
		return
	}
	var in openapi.CloseIssueInput
	if !decodeIssueJSON(w, r, &in, true) {
		return
	}
	reason := openapi.Completed
	if in.Reason != nil {
		reason = *in.Reason
	}
	if !reason.Valid() {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "reason: completed, not_planned o duplicate.")
		return
	}
	var duplicateOf *int64
	switch {
	case reason == openapi.Duplicate && in.DuplicateOf == nil:
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "duplicateOf è obbligatorio con reason=duplicate.")
		return
	case reason != openapi.Duplicate && in.DuplicateOf != nil:
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "duplicateOf è ammesso solo con reason=duplicate.")
		return
	case reason == openapi.Duplicate:
		if *in.DuplicateOf < 1 || *in.DuplicateOf == number {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "duplicateOf deve essere un'altra issue del repo.")
			return
		}
		duplicateOf = in.DuplicateOf
	}

	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if duplicateOf != nil {
		// Un'altra issue dello stesso repo; una nascosta non esiste per chi
		// non è admin.
		var hidden bool
		err := tx.QueryRow(ctx, `SELECT hidden FROM core.issues WHERE repo_id = $1 AND number = $2`, ia.repo.ID, *duplicateOf).Scan(&hidden)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && hidden && !ia.admin) {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "duplicateOf non è una issue di questo repo.")
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura della issue duplicata non riuscita", err)
			return
		}
	}
	if x.State == "closed" {
		writeError(w, http.StatusConflict, "already_closed", "La issue è già chiusa.")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.issues SET state = 'closed', close_reason = $2, duplicate_of = $3,
		closed_at = now(), updated_at = now() WHERE id = $1`, x.ID, string(reason), duplicateOf); err != nil {
		writeIssueFailure(w, "chiusura della issue non riuscita", err)
		return
	}
	data := map[string]any{"reason": string(reason)}
	if duplicateOf != nil {
		data["duplicateOf"] = *duplicateOf
	}
	if err := insertIssueEvent(ctx, tx, x.ID, "closed", ia.userID, data); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della chiusura non riuscito", err)
		return
	}
	s.respondIssue(w, ctx, http.StatusOK, ia, number, nil)
}

// ReopenIssue implementa POST .../reopen: azzera motivo e duplicato (I2).
func (s *apiServer) ReopenIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	cur, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok || ia.rejectArchived(w) {
		return
	}
	if !ia.write && cur.AuthorID != ia.userID {
		writeError(w, http.StatusForbidden, "forbidden", "Riapre la issue chi ha write o l'autore.")
		return
	}
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if x.State == "open" {
		writeError(w, http.StatusConflict, "already_open", "La issue è già aperta.")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.issues SET state = 'open', close_reason = NULL, duplicate_of = NULL,
		closed_at = NULL, updated_at = now() WHERE id = $1`, x.ID); err != nil {
		writeIssueFailure(w, "riapertura della issue non riuscita", err)
		return
	}
	if err := insertIssueEvent(ctx, tx, x.ID, "reopened", ia.userID, nil); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della riapertura non riuscito", err)
		return
	}
	s.respondIssue(w, ctx, http.StatusOK, ia, number, nil)
}

// SetIssueHidden implementa PUT .../hidden (I4): solo admin.
func (s *apiServer) SetIssueHidden(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if _, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false); !ok || ia.rejectArchived(w) {
		return
	}
	if !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Nascondere una issue richiede il permesso admin.")
		return
	}
	var in struct {
		Hidden *bool `json:"hidden"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Hidden == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "hidden è obbligatorio.")
		return
	}
	hidden := *in.Hidden
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if x.Hidden != hidden {
		if _, err := tx.Exec(ctx, `UPDATE core.issues SET hidden = $2, updated_at = now() WHERE id = $1`, x.ID, hidden); err != nil {
			writeIssueFailure(w, "modifica della issue non riuscita", err)
			return
		}
		typ := "hidden"
		if !hidden {
			typ = "unhidden"
		}
		if err := insertIssueEvent(ctx, tx, x.ID, typ, ia.userID, nil); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondIssue(w, ctx, http.StatusOK, ia, number, nil)
}

// ListIssueVersions implementa GET .../versions (I4): solo admin.
func (s *apiServer) ListIssueVersions(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	x, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok {
		return
	}
	if !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Le versioni precedenti sono visibili con il permesso admin.")
		return
	}
	ctx := r.Context()
	rows, err := s.pool.Query(ctx, `SELECT version, title, body, editor_id, created_at FROM core.issue_text_versions
		WHERE issue_id = $1 AND comment_id IS NULL ORDER BY version DESC`, x.ID)
	if err != nil {
		writeIssueFailure(w, "lettura delle versioni non riuscita", err)
		return
	}
	var out []openapi.TextVersion
	var editors []uuid.UUID
	var editorOf []uuid.UUID
	for rows.Next() {
		var tv openapi.TextVersion
		var editor uuid.UUID
		if err := rows.Scan(&tv.Version, &tv.Title, &tv.Body, &editor, &tv.CreatedAt); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle versioni non riuscita", err)
			return
		}
		out = append(out, tv)
		editors = append(editors, editor)
		editorOf = append(editorOf, editor)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle versioni non riuscita", err)
		return
	}
	dir, err := s.resolveUsers(ctx, editors)
	if err != nil {
		writeIssueFailure(w, "risoluzione degli utenti non riuscita", err)
		return
	}
	items := make([]openapi.TextVersion, 0, len(out))
	for i, tv := range out {
		tv.Editor = dir[editorOf[i]]
		items = append(items, tv)
	}
	writeJSON(w, http.StatusOK, openapi.TextVersionList{Items: items})
}

// ListIssueEvents implementa GET .../events: la cronologia in ordine
// crescente.
func (s *apiServer) ListIssueEvents(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam, params openapi.ListIssueEventsParams) {
	page, perPage := defaultPage, defaultPerPage
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	x, ok := s.issueFor(w, ctx, s.pool, ia, number, false)
	if !ok {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.issue_events WHERE issue_id = $1`, x.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio degli eventi non riuscito", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT id, type, actor_id, data, created_at FROM core.issue_events
		WHERE issue_id = $1 ORDER BY seq LIMIT $2 OFFSET $3`, x.ID, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura degli eventi non riuscita", err)
		return
	}
	var evs []openapi.IssueEvent
	var actors []*uuid.UUID
	var actorIDs []uuid.UUID
	for rows.Next() {
		var ev openapi.IssueEvent
		var id uuid.UUID
		var typ string
		var actor *uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &typ, &actor, &raw, &ev.CreatedAt); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura degli eventi non riuscita", err)
			return
		}
		ev.Id, ev.Type = openapi_types.UUID(id), openapi.IssueEventType(typ)
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			writeIssueFailure(w, "dati dell'evento non validi", err)
			return
		}
		ev.Data = &data
		evs = append(evs, ev)
		actors = append(actors, actor)
		if actor != nil {
			actorIDs = append(actorIDs, *actor)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura degli eventi non riuscita", err)
		return
	}
	dir, err := s.resolveUsers(ctx, actorIDs)
	if err != nil {
		writeIssueFailure(w, "risoluzione degli utenti non riuscita", err)
		return
	}
	items := make([]openapi.IssueEvent, 0, len(evs))
	for i, ev := range evs {
		if actors[i] != nil {
			u := dir[*actors[i]]
			ev.Actor = &u
		}
		items = append(items, ev)
	}
	writeJSON(w, http.StatusOK, openapi.IssueEventList{Items: items, Page: page, PerPage: perPage, Total: total})
}
