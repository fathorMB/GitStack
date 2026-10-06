package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Commenti, blocco della discussione ed eliminazione tracciata (M-05/D,
// GIT-104): regole I3, I4, I11 e R10. Le issue nascoste rispondono 404 a chi
// non ha admin (I4), come nel resto delle issues.

const maxLockReasonRunes = 256

func writeLocked(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "locked", "La discussione è bloccata: commenta solo chi ha write.")
}

func writeCommentNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Commento non trovato.")
}

// writeAttachmentNotLinkable: un allegato di attachmentIds non esiste, è di
// un altro repo o di un altro utente, o è già collegato.
func writeAttachmentNotLinkable(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "attachment_not_linkable",
		"Uno degli allegati non esiste in questo repo, non è tuo o è già collegato.")
}

func attachmentIDs(in *[]openapi_types.UUID) []uuid.UUID {
	if in == nil {
		return nil
	}
	out := make([]uuid.UUID, len(*in))
	for i, id := range *in {
		out[i] = uuid.UUID(id)
	}
	return out
}

// commentRow è una riga di core.issue_comments.
type commentRow struct {
	ID           uuid.UUID
	AuthorID     uuid.UUID
	Body         string
	Edited       bool
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ViaTokenID   *uuid.UUID
	ViaTokenName *string
}

const commentCols = `c.id, c.author_id, c.body, c.edited, c.deleted_at, c.created_at, c.updated_at, c.via_token_id, c.via_token_name`

func scanComment(r pgx.Row) (commentRow, error) {
	var c commentRow
	err := r.Scan(&c.ID, &c.AuthorID, &c.Body, &c.Edited, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt, &c.ViaTokenID, &c.ViaTokenName)
	return c, err
}

// getComment legge un commento della issue; forUpdate lo blocca.
// store.ErrNotFound se non c'è o è di un'altra issue.
func getComment(ctx context.Context, q querier, issueID, id uuid.UUID, forUpdate bool) (commentRow, error) {
	sql := `SELECT ` + commentCols + ` FROM core.issue_comments c WHERE c.issue_id = $1 AND c.id = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF c`
	}
	c, err := scanComment(q.QueryRow(ctx, sql, issueID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return commentRow{}, store.ErrNotFound
	}
	return c, err
}

// issueAttachments sono gli allegati collegati a una issue: quelli della issue
// (chiave uuid.Nil) e quelli di ciascun commento.
func (s *apiServer) issueAttachments(ctx context.Context, q querier, ia issueAccess, issueID uuid.UUID) (map[uuid.UUID][]openapi.IssueAttachment, error) {
	rows, err := q.Query(ctx, `SELECT id, comment_id, filename, content_type, size_bytes, created_at
		FROM core.issue_attachments WHERE issue_id = $1 ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]openapi.IssueAttachment{}
	for rows.Next() {
		var a store.Attachment
		var commentID *uuid.UUID
		if err := rows.Scan(&a.ID, &commentID, &a.Filename, &a.ContentType, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		key := uuid.Nil
		if commentID != nil {
			key = *commentID
		}
		out[key] = append(out[key], toAPIAttachment(ia.repo.OwnerName, ia.repo.Name, a))
	}
	return out, rows.Err()
}

// commentViews compone IssueComment: autori da identity, allegati dal DB. Un
// commento eliminato ha testo vuoto e nessun allegato.
func (s *apiServer) commentViews(ctx context.Context, q querier, ia issueAccess, x issueRow, list []commentRow) ([]openapi.IssueComment, error) {
	ids := make([]uuid.UUID, len(list))
	for i, c := range list {
		ids[i] = c.AuthorID
	}
	dir, err := s.resolveUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	atts, err := s.issueAttachments(ctx, q, ia, x.ID)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.IssueComment, 0, len(list))
	for _, c := range list {
		v := openapi.IssueComment{
			Id: openapi_types.UUID(c.ID), IssueNumber: x.Number, Author: dir[c.AuthorID], ViaToken: viaToken(c.ViaTokenID, c.ViaTokenName), Edited: c.Edited,
			Deleted: c.DeletedAt != nil, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		}
		if c.DeletedAt == nil {
			v.Body = c.Body
			if a := atts[c.ID]; len(a) > 0 {
				v.Attachments = &a
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *apiServer) respondComment(w http.ResponseWriter, ctx context.Context, status int, ia issueAccess, x issueRow, id uuid.UUID, headers map[string]string) {
	c, err := getComment(ctx, s.pool, x.ID, id, false)
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	views, err := s.commentViews(ctx, s.pool, ia, x, []commentRow{c})
	if err != nil {
		writeIssueFailure(w, "composizione del commento non riuscita", err)
		return
	}
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	writeJSON(w, status, views[0])
}

func commentBodyError(body string) string {
	if strings.TrimSpace(body) == "" {
		return "body: obbligatorio."
	}
	if utf8.RuneCountInString(body) > maxIssueBodyRunes {
		return "body: al massimo 65536 caratteri."
	}
	return ""
}

// ListIssueComments implementa GET .../comments: dal più vecchio al più
// recente; un commento eliminato resta con deleted=true e senza testo.
func (s *apiServer) ListIssueComments(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam, params openapi.ListIssueCommentsParams) {
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
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.issue_comments WHERE issue_id = $1`, x.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio dei commenti non riuscito", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+commentCols+` FROM core.issue_comments c WHERE c.issue_id = $1
		ORDER BY c.created_at, c.id LIMIT $2 OFFSET $3`, x.ID, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura dei commenti non riuscita", err)
		return
	}
	var list []commentRow
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura dei commenti non riuscita", err)
			return
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura dei commenti non riuscita", err)
		return
	}
	items, err := s.commentViews(ctx, s.pool, ia, x, list)
	if err != nil {
		writeIssueFailure(w, "composizione dei commenti non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.IssueCommentList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// CreateIssueComment implementa POST .../comments: chi vede il repo commenta
// (I3); con la discussione bloccata solo chi ha write (I11). Gli allegati si
// collegano nella stessa transazione del commento.
func (s *apiServer) CreateIssueComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if _, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false); !ok || ia.rejectArchived(w) {
		return
	}
	var in openapi.CreateIssueCommentInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if msg := commentBodyError(in.Body); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
		return
	}
	if in.AttachmentIds != nil && len(*in.AttachmentIds) > 20 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "attachmentIds: al massimo 20.")
		return
	}
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if x.Locked && !ia.write {
		writeLocked(w)
		return
	}
	id := uuid.New()
	viaID, viaName := tokenOrigin(ia.caller)
	if _, err := tx.Exec(ctx, `INSERT INTO core.issue_comments (id, issue_id, author_id, body, created_at, updated_at, via_token_id, via_token_name)
		VALUES ($1, $2, $3, $4, clock_timestamp(), clock_timestamp(), $5, $6)`, id, x.ID, ia.userID, in.Body, viaID, viaName); err != nil {
		writeIssueFailure(w, "creazione del commento non riuscita", err)
		return
	}
	if err := store.LinkAttachments(ctx, tx, ia.repo.ID, ia.userID, x.ID, &id, attachmentIDs(in.AttachmentIds)); err != nil {
		if errors.Is(err, store.ErrAttachmentNotLinkable) {
			writeAttachmentNotLinkable(w)
			return
		}
		writeIssueFailure(w, "collegamento degli allegati non riuscito", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.issues SET updated_at = now() WHERE id = $1`, x.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della issue non riuscito", err)
		return
	}
	if err := emitComment(ctx, tx, domainevents.IssueCommentCreated, x.ID, ia.userID,
		domainevents.Comment{ID: id.String(), AuthorID: ia.userID.String(), Body: in.Body}, nil); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	// C1: riferimenti nel commento (issue_references).
	// Togliere un riferimento dal testo non cancella le tracce esistenti:
	// come su GitHub, le righe in core.issue_references restano.
	commentID := id
	if err := s.processReferences(ctx, tx, ia.userID, ia.repo.ID, x.Number, x.ID, &commentID, "issue", x.Title, in.Body); err != nil {
		writeIssueFailure(w, "elaborazione dei riferimenti non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit del commento non riuscito", err)
		return
	}
	loc := fmt.Sprintf("/repos/%s/%s/issues/%d/comments/%s", ia.repo.OwnerName, ia.repo.Name, number, id)
	s.respondComment(w, ctx, http.StatusCreated, ia, x, id, map[string]string{"Location": loc})
}

// UpdateIssueComment implementa PATCH .../comments/{commentId}: solo
// l'autore (I4); la versione sostituita si conserva.
func (s *apiServer) UpdateIssueComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam, commentID openapi.IssueCommentIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	x, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok {
		return
	}
	cur, err := getComment(r.Context(), s.pool, x.ID, uuid.UUID(commentID), false)
	if errors.Is(err, store.ErrNotFound) || (err == nil && cur.DeletedAt != nil) {
		writeCommentNotFound(w)
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	if ia.rejectArchived(w) {
		return
	}
	if cur.AuthorID != ia.userID {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'autore modifica il proprio commento.")
		return
	}
	var in openapi.UpdateIssueCommentInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if msg := commentBodyError(in.Body); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", msg)
		return
	}
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if x.Locked && !ia.write {
		writeLocked(w)
		return
	}
	c, err := getComment(ctx, tx, x.ID, uuid.UUID(commentID), true)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.DeletedAt != nil) {
		writeCommentNotFound(w)
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	if in.Body != c.Body {
		// La versione sostituita: 1 è il testo originale.
		if _, err := tx.Exec(ctx, `INSERT INTO core.issue_text_versions (id, issue_id, comment_id, version, body, editor_id)
			VALUES ($1, $2, $3, (SELECT COALESCE(max(version), 0) + 1 FROM core.issue_text_versions WHERE comment_id = $3), $4, $5)`,
			uuid.New(), x.ID, c.ID, c.Body, ia.userID); err != nil {
			writeIssueFailure(w, "salvataggio della versione non riuscito", err)
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE core.issue_comments SET body = $2, edited = true, updated_at = now() WHERE id = $1`, c.ID, in.Body); err != nil {
			writeIssueFailure(w, "modifica del commento non riuscita", err)
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE core.issues SET updated_at = now() WHERE id = $1`, x.ID); err != nil {
			writeIssueFailure(w, "aggiornamento della issue non riuscito", err)
			return
		}
		if err := emitComment(ctx, tx, domainevents.IssueCommentEdited, x.ID, ia.userID,
			domainevents.Comment{ID: c.ID.String(), AuthorID: c.AuthorID.String(), Body: in.Body},
			&domainevents.CommentChanges{Body: &domainevents.From{From: c.Body}}); err != nil {
			writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
			return
		}
		// C1: riferimenti nel commento modificato (issue_references).
		// Togliere un riferimento dal testo non cancella le tracce esistenti:
		// come su GitHub, le righe in core.issue_references restano.
		commentID := c.ID
		if err := s.processReferences(ctx, tx, ia.userID, ia.repo.ID, x.Number, x.ID, &commentID, "issue", x.Title, in.Body); err != nil {
			writeIssueFailure(w, "elaborazione dei riferimenti non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondComment(w, ctx, http.StatusOK, ia, x, c.ID, nil)
}

// DeleteIssueComment implementa DELETE .../comments/{commentId}: l'autore o
// admin (I4). Il testo si svuota (il precedente resta tra le versioni, per
// admin) e la cronologia riceve comment_deleted.
func (s *apiServer) DeleteIssueComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam, commentID openapi.IssueCommentIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	x, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	if !ok {
		return
	}
	cur, err := getComment(r.Context(), s.pool, x.ID, uuid.UUID(commentID), false)
	if errors.Is(err, store.ErrNotFound) || (err == nil && cur.DeletedAt != nil) {
		writeCommentNotFound(w)
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	if ia.rejectArchived(w) {
		return
	}
	if cur.AuthorID != ia.userID && !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Elimina un commento l'autore o chi ha admin.")
		return
	}
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := getComment(ctx, tx, x.ID, uuid.UUID(commentID), true)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.DeletedAt != nil) {
		writeCommentNotFound(w)
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	// Il testo eliminato resta visibile ad admin tra le versioni.
	if _, err := tx.Exec(ctx, `INSERT INTO core.issue_text_versions (id, issue_id, comment_id, version, body, editor_id)
		VALUES ($1, $2, $3, (SELECT COALESCE(max(version), 0) + 1 FROM core.issue_text_versions WHERE comment_id = $3), $4, $5)`,
		uuid.New(), x.ID, c.ID, c.Body, ia.userID); err != nil {
		writeIssueFailure(w, "salvataggio della versione non riuscito", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.issue_comments SET body = '', deleted_at = now(), updated_at = now() WHERE id = $1`, c.ID); err != nil {
		writeIssueFailure(w, "eliminazione del commento non riuscita", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.issues SET updated_at = now() WHERE id = $1`, x.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della issue non riuscito", err)
		return
	}
	if err := insertIssueEvent(ctx, tx, x.ID, "comment_deleted", ia.userID, map[string]any{"commentId": c.ID.String()}); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	// Senza testo (I4): l'evento dice solo quale commento è sparito.
	if err := emitComment(ctx, tx, domainevents.IssueCommentDeleted, x.ID, ia.userID,
		domainevents.Comment{ID: c.ID.String(), AuthorID: c.AuthorID.String()}, nil); err != nil {
		writeIssueFailure(w, "scrittura dell'evento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit dell'eliminazione non riuscito", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListIssueCommentVersions implementa GET .../comments/{commentId}/versions:
// solo admin (I4), anche per i commenti eliminati.
func (s *apiServer) ListIssueCommentVersions(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam, commentID openapi.IssueCommentIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	x, ok := s.issueFor(w, ctx, s.pool, ia, number, false)
	if !ok {
		return
	}
	if !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Le versioni precedenti sono visibili con il permesso admin.")
		return
	}
	c, err := getComment(ctx, s.pool, x.ID, uuid.UUID(commentID), false)
	if errors.Is(err, store.ErrNotFound) {
		writeCommentNotFound(w)
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT version, body, editor_id, created_at FROM core.issue_text_versions
		WHERE comment_id = $1 ORDER BY version DESC`, c.ID)
	if err != nil {
		writeIssueFailure(w, "lettura delle versioni non riuscita", err)
		return
	}
	var out []openapi.TextVersion
	var editors []uuid.UUID
	for rows.Next() {
		var tv openapi.TextVersion
		var editor uuid.UUID
		if err := rows.Scan(&tv.Version, &tv.Body, &editor, &tv.CreatedAt); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle versioni non riuscita", err)
			return
		}
		out = append(out, tv)
		editors = append(editors, editor)
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
		tv.Editor = dir[editors[i]]
		items = append(items, tv)
	}
	writeJSON(w, http.StatusOK, openapi.TextVersionList{Items: items})
}

// LockIssue implementa PUT .../lock (I11): solo admin; idempotente.
func (s *apiServer) LockIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	s.setLocked(w, r, owner, name, number, true)
}

// UnlockIssue implementa DELETE .../lock (I11): solo admin; idempotente.
func (s *apiServer) UnlockIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	s.setLocked(w, r, owner, name, number, false)
}

func (s *apiServer) setLocked(w http.ResponseWriter, r *http.Request, owner, name string, number int64, lock bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	if _, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false); !ok || ia.rejectArchived(w) {
		return
	}
	if !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Bloccare o sbloccare la discussione richiede il permesso admin.")
		return
	}
	reason := ""
	if lock {
		var in openapi.LockIssueInput
		if !decodeIssueJSON(w, r, &in, true) {
			return
		}
		if in.Reason != nil {
			reason = strings.TrimSpace(*in.Reason)
		}
		if utf8.RuneCountInString(reason) > maxLockReasonRunes {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "reason: al massimo 256 caratteri.")
			return
		}
	}
	ctx := r.Context()
	tx, x, ok := s.beginIssueWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if x.Locked != lock {
		if _, err := tx.Exec(ctx, `UPDATE core.issues SET locked = $2, lock_reason = $3, updated_at = now() WHERE id = $1`, x.ID, lock, reason); err != nil {
			writeIssueFailure(w, "modifica della issue non riuscita", err)
			return
		}
		typ, data := "unlocked", map[string]any(nil)
		if lock {
			typ = "locked"
			if reason != "" {
				data = map[string]any{"reason": reason}
			}
		}
		if err := insertIssueEvent(ctx, tx, x.ID, typ, ia.userID, data); err != nil {
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

// issueWithAttachments compone la issue con i suoi allegati (quelli dei
// commenti stanno nei commenti).
func (s *apiServer) issueWithAttachments(ctx context.Context, ia issueAccess, v issueViews, x issueRow) (openapi.Issue, error) {
	iss := v.issue(x)
	atts, err := s.issueAttachments(ctx, s.pool, ia, x.ID)
	if err != nil {
		return iss, err
	}
	if a := atts[uuid.Nil]; len(a) > 0 {
		iss.Attachments = &a
	}
	return iss, nil
}
