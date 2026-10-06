package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Casella delle notifiche in-app (M-06/E, GIT-133; regole C3, C4, C9, I8).
// La casella è dell'utente corrente, persona o agente (C4): nessun percorso
// con lo username. Le notifiche le scrive il motore (internal/notify); qui si
// leggono e si gestiscono.
//
// Accesso (C9, I8): prima di ogni lettura o modifica in blocco si verifica
// con identity che l'utente legga ancora i repo delle sue notifiche; quelle
// dei repo che non legge più si eliminano, quelle di una issue nascosta
// restano solo a chi è admin del repo. Così nessuna risposta contiene titolo,
// motivo o nome di un repo che il destinatario non può leggere.

var notificationReasons = map[string]bool{
	"assigned": true, "mentioned": true, "participating": true, "subscribed": true,
	"commit_linked": true, "state_change": true, "webhook": true,
}

type inboxFilter struct {
	reasons             []string
	repoOwner, repoName string
	hasRepo             bool
}

// parseInboxFilter valida `reason` e `repo`; risponde 400 e ritorna false.
func parseInboxFilter(w http.ResponseWriter, reason, repo *string) (inboxFilter, bool) {
	var f inboxFilter
	if reason != nil && *reason != "" {
		for _, p := range strings.Split(*reason, ",") {
			p = strings.TrimSpace(p)
			if !notificationReasons[p] {
				writeError(w, http.StatusBadRequest, "invalid_reason", "reason: motivo sconosciuto «"+p+"».")
				return f, false
			}
			f.reasons = append(f.reasons, p)
		}
	}
	if repo != nil && *repo != "" {
		owner, name, ok := strings.Cut(strings.ToLower(*repo), "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			writeError(w, http.StatusBadRequest, "invalid_repo", "repo deve essere owner/repo.")
			return f, false
		}
		f.repoOwner, f.repoName, f.hasRepo = owner, name, true
	}
	return f, true
}

// inboxScope: dopo la verifica degli accessi, i repo in cui l'utente non è
// admin (le notifiche di issue nascoste lì non si mostrano).
type inboxScope struct {
	userID   uuid.UUID
	nonAdmin []uuid.UUID
}

func (s *apiServer) inboxReady(w http.ResponseWriter) bool {
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sulle notifiche.")
		return false
	}
	return true
}

// scopeFor verifica gli accessi dell'utente ai repo delle sue notifiche ed
// elimina quelle dei repo che non legge più (C9). Risponde 503 se identity
// non risponde (mai una casella non filtrata).
func (s *apiServer) scopeFor(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (inboxScope, bool) {
	ctx := r.Context()
	sc := inboxScope{userID: userID}
	rows, err := s.pool.Query(ctx, `SELECT n.repo_id, bool_or(COALESCE(i.hidden, false))
		FROM core.notifications n LEFT JOIN core.issues i ON i.id = n.issue_id
		WHERE n.user_id = $1 AND n.repo_id IS NOT NULL GROUP BY n.repo_id`, userID)
	if err != nil {
		writeIssueFailure(w, "lettura delle notifiche non riuscita", err)
		return sc, false
	}
	type repoRef struct {
		id     uuid.UUID
		hidden bool
	}
	var refs []repoRef
	for rows.Next() {
		var ref repoRef
		if err := rows.Scan(&ref.id, &ref.hidden); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle notifiche non riuscita", err)
			return sc, false
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle notifiche non riuscita", err)
		return sc, false
	}
	var gone []uuid.UUID
	for _, ref := range refs {
		can, err := s.repoIdentity.HasRole(ctx, userID, ref.id, "read")
		if err == nil && !can {
			gone = append(gone, ref.id)
			continue
		}
		if err == nil && ref.hidden {
			var admin bool
			if admin, err = s.repoIdentity.HasRole(ctx, userID, ref.id, "admin"); err == nil && !admin {
				sc.nonAdmin = append(sc.nonAdmin, ref.id)
			}
		}
		if err != nil {
			slog.Default().Warn("verifica dell'accesso per le notifiche non riuscita", "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
			return sc, false
		}
	}
	if len(gone) > 0 {
		if _, err := s.pool.Exec(ctx, `DELETE FROM core.notifications WHERE user_id = $1 AND repo_id = ANY($2)`, userID, gone); err != nil {
			writeIssueFailure(w, "eliminazione delle notifiche non riuscita", err)
			return sc, false
		}
	}
	return sc, true
}

const inboxFrom = ` FROM core.notifications n
	LEFT JOIN core.repositories r ON r.resource_id = n.repo_id
	LEFT JOIN core.issues i ON i.id = n.issue_id`

// inboxWhere compone la clausola comune: dell'utente, repo non eliminato,
// niente issue nascoste dove non è admin, filtri reason e repo. args parte
// già con userID e nonAdmin.
func inboxWhere(sc inboxScope, f inboxFilter) (string, []any) {
	nonAdmin := sc.nonAdmin
	if nonAdmin == nil {
		nonAdmin = []uuid.UUID{}
	}
	args := []any{sc.userID, nonAdmin}
	where := ` WHERE n.user_id = $1
		AND (n.repo_id IS NULL OR (r.resource_id IS NOT NULL AND r.deleted_at IS NULL))
		AND NOT (COALESCE(i.hidden, false) AND n.repo_id = ANY($2::uuid[]))`
	if len(f.reasons) > 0 {
		args = append(args, f.reasons)
		where += fmt.Sprintf(" AND n.reason = ANY($%d::text[])", len(args))
	}
	if f.hasRepo {
		args = append(args, f.repoOwner, f.repoName)
		where += fmt.Sprintf(" AND lower(r.owner_name) = lower($%d) AND lower(r.name) = lower($%d)", len(args)-1, len(args))
	}
	return where, args
}

const unreadCond = ` AND n.read_at IS NULL AND n.archived_at IS NULL`

func stateCond(state string) (string, bool) {
	switch state {
	case "unread":
		return unreadCond, true
	case "read":
		return ` AND n.read_at IS NOT NULL AND n.archived_at IS NULL`, true
	case "archived":
		return ` AND n.archived_at IS NOT NULL`, true
	case "all":
		return "", true
	}
	return "", false
}

// callerForInbox risolve il chiamante e controlla che identity ci sia.
func (s *apiServer) callerForInbox(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if !s.inboxReady(w) {
		return uuid.Nil, false
	}
	return userID, true
}

// ListNotifications implementa GET /notifications.
func (s *apiServer) ListNotifications(w http.ResponseWriter, r *http.Request, params openapi.ListNotificationsParams) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	f, ok := parseInboxFilter(w, params.Reason, params.Repo)
	if !ok {
		return
	}
	state := "unread"
	if params.State != nil {
		state = string(*params.State)
	}
	cond, ok := stateCond(state)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_state", "state: unread, read, archived o all.")
		return
	}
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
	sc, ok := s.scopeFor(w, r, userID)
	if !ok {
		return
	}
	ctx := r.Context()
	where, args := inboxWhere(sc, f)
	var total, unread int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+inboxFrom+where+cond, args...).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle notifiche non riuscito", err)
		return
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+inboxFrom+where+unreadCond, args...).Scan(&unread); err != nil {
		writeIssueFailure(w, "conteggio delle notifiche non riuscito", err)
		return
	}
	qargs := append(append([]any{}, args...), perPage, (page-1)*perPage)
	rows, err := s.pool.Query(ctx, notificationSelect+inboxFrom+inboxWebhookJoin+where+cond+
		fmt.Sprintf(` ORDER BY n.created_at DESC, n.id LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2), qargs...)
	if err != nil {
		writeIssueFailure(w, "lettura delle notifiche non riuscita", err)
		return
	}
	items, err := s.scanNotifications(ctx, rows)
	if err != nil {
		writeIssueFailure(w, "lettura delle notifiche non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.NotificationList{Items: items, Page: page, PerPage: perPage, Total: total, UnreadCount: unread})
}

const inboxWebhookJoin = ` LEFT JOIN core.webhooks wh ON wh.id = n.webhook_id`

const notificationSelect = `SELECT n.id, n.reason, n.read_at, n.archived_at, n.event_name, n.created_at, n.comment_id, n.actor_id,
	n.repo_id, r.owner_name, r.name, i.number, i.title, i.state, n.data, n.webhook_id, wh.scope, wh.url`

type notificationRow struct {
	id                  uuid.UUID
	reason              string
	readAt, archivedAt  *time.Time
	event               string
	createdAt           time.Time
	commentID, actorID  *uuid.UUID
	repoID              *uuid.UUID
	owner, repoName     *string
	number              *int64
	title, state        *string
	data                []byte
	webhookID           *uuid.UUID
	webhookScope, wbURL *string
}

func (s *apiServer) scanNotifications(ctx context.Context, rows pgx.Rows) ([]openapi.Notification, error) {
	var list []notificationRow
	for rows.Next() {
		var n notificationRow
		if err := rows.Scan(&n.id, &n.reason, &n.readAt, &n.archivedAt, &n.event, &n.createdAt, &n.commentID, &n.actorID,
			&n.repoID, &n.owner, &n.repoName, &n.number, &n.title, &n.state, &n.data, &n.webhookID, &n.webhookScope, &n.wbURL); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, n := range list {
		if n.actorID != nil {
			ids = append(ids, *n.actorID)
		}
	}
	dir, err := s.resolveUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.Notification, 0, len(list))
	for _, n := range list {
		out = append(out, toAPINotification(n, dir))
	}
	return out, nil
}

func toAPINotification(n notificationRow, dir userDirectory) openapi.Notification {
	o := openapi.Notification{
		Id: openapi_types.UUID(n.id), Reason: openapi.NotificationReason(n.reason), Read: n.readAt != nil, Archived: n.archivedAt != nil,
		Event: n.event, CreatedAt: n.createdAt, ReadAt: n.readAt,
	}
	if n.commentID != nil {
		c := openapi_types.UUID(*n.commentID)
		o.CommentId = &c
	}
	repoFull := ""
	if n.repoID != nil && n.owner != nil && n.repoName != nil {
		repoFull = *n.owner + "/" + *n.repoName
		o.Repository = &openapi.NotificationRepo{Id: openapi_types.UUID(*n.repoID), FullName: repoFull}
	}
	if n.number != nil && n.title != nil && n.state != nil {
		o.Issue = &openapi.NotificationIssue{Number: *n.number, Title: *n.title, State: openapi.NotificationIssueState(*n.state)}
	}
	if n.webhookID != nil && n.webhookScope != nil && n.wbURL != nil {
		o.Webhook = &openapi.NotificationWebhook{Id: openapi_types.UUID(*n.webhookID), Scope: openapi.WebhookScope(*n.webhookScope), Url: *n.wbURL}
	}
	actor := ""
	if n.actorID != nil {
		if u, ok := dir[*n.actorID]; ok {
			u := u
			o.Actor = &u
			actor = u.Username
		}
	}
	var data map[string]any
	_ = json.Unmarshal(n.data, &data)
	o.Summary = notificationSummary(n.reason, n.event, actor, repoFull, n.number, n.title, data)
	return o
}

// notificationSummary è il testo breve della casella. Usa il titolo corrente
// della issue, già filtrato per accesso: mai dati di un repo non leggibile.
func notificationSummary(reason, event, actor, repo string, number *int64, title *string, data map[string]any) string {
	who := actor
	if who == "" {
		who = "GitStack"
	}
	where := repo
	if number != nil {
		where = fmt.Sprintf("%s#%d", repo, *number)
	}
	if title != nil {
		where += ": " + *title
	}
	switch reason {
	case "assigned":
		return who + " ti ha assegnato " + where
	case "mentioned":
		return who + " ti ha menzionato in " + where
	case "state_change":
		if event == "issue.reopened" {
			return who + " ha riaperto " + where
		}
		return who + " ha chiuso " + where
	case "commit_linked":
		return "Un commit cita " + where
	case "webhook":
		return "Un webhook che gestisci è stato disattivato dai fallimenti"
	}
	switch event {
	case "issue.created":
		return who + " ha aperto " + where
	case "issue_comment.created":
		return who + " ha commentato " + where
	}
	return who + ": " + where
}

// getOwnNotification legge una notifica visibile al chiamante: dell'utente, di
// un repo che legge. Risponde 404 (come una inesistente) altrimenti.
func (s *apiServer) getOwnNotification(w http.ResponseWriter, r *http.Request, userID uuid.UUID, id uuid.UUID) (openapi.Notification, bool) {
	sc, ok := s.scopeFor(w, r, userID)
	if !ok {
		return openapi.Notification{}, false
	}
	where, args := inboxWhere(sc, inboxFilter{})
	args = append(args, id)
	rows, err := s.pool.Query(r.Context(), notificationSelect+inboxFrom+inboxWebhookJoin+where+fmt.Sprintf(" AND n.id = $%d", len(args)), args...)
	if err != nil {
		writeIssueFailure(w, "lettura della notifica non riuscita", err)
		return openapi.Notification{}, false
	}
	items, err := s.scanNotifications(r.Context(), rows)
	if err != nil {
		writeIssueFailure(w, "lettura della notifica non riuscita", err)
		return openapi.Notification{}, false
	}
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Notifica non trovata.")
		return openapi.Notification{}, false
	}
	return items[0], true
}

// GetNotification implementa GET /notifications/{notificationId}.
func (s *apiServer) GetNotification(w http.ResponseWriter, r *http.Request, id openapi.NotificationIdParam) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	if n, ok := s.getOwnNotification(w, r, userID, uuid.UUID(id)); ok {
		writeJSON(w, http.StatusOK, n)
	}
}

// UpdateNotification implementa PATCH /notifications/{notificationId}: read
// e/o archived, idempotente.
func (s *apiServer) UpdateNotification(w http.ResponseWriter, r *http.Request, id openapi.NotificationIdParam) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	var in struct {
		Read     *bool `json:"read"`
		Archived *bool `json:"archived"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Read == nil && in.Archived == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Serve almeno uno fra read e archived.")
		return
	}
	if _, ok := s.getOwnNotification(w, r, userID, uuid.UUID(id)); !ok {
		return
	}
	now := s.now()
	if in.Read != nil {
		q := `UPDATE core.notifications SET read_at = NULL WHERE id = $1 AND user_id = $2`
		args := []any{uuid.UUID(id), userID}
		if *in.Read {
			q = `UPDATE core.notifications SET read_at = COALESCE(read_at, $3) WHERE id = $1 AND user_id = $2`
			args = append(args, now)
		}
		if _, err := s.pool.Exec(r.Context(), q, args...); err != nil {
			writeIssueFailure(w, "aggiornamento della notifica non riuscito", err)
			return
		}
	}
	if in.Archived != nil {
		q := `UPDATE core.notifications SET archived_at = NULL WHERE id = $1 AND user_id = $2`
		args := []any{uuid.UUID(id), userID}
		if *in.Archived {
			q = `UPDATE core.notifications SET archived_at = COALESCE(archived_at, $3) WHERE id = $1 AND user_id = $2`
			args = append(args, now)
		}
		if _, err := s.pool.Exec(r.Context(), q, args...); err != nil {
			writeIssueFailure(w, "aggiornamento della notifica non riuscito", err)
			return
		}
	}
	if n, ok := s.getOwnNotification(w, r, userID, uuid.UUID(id)); ok {
		writeJSON(w, http.StatusOK, n)
	}
}

// DeleteNotification implementa DELETE /notifications/{notificationId}.
func (s *apiServer) DeleteNotification(w http.ResponseWriter, r *http.Request, id openapi.NotificationIdParam) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	if _, ok := s.getOwnNotification(w, r, userID, uuid.UUID(id)); !ok {
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM core.notifications WHERE id = $1 AND user_id = $2`, uuid.UUID(id), userID); err != nil {
		writeIssueFailure(w, "eliminazione della notifica non riuscita", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteNotifications implementa DELETE /notifications?state=read|archived.
func (s *apiServer) DeleteNotifications(w http.ResponseWriter, r *http.Request, params openapi.DeleteNotificationsParams) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	state := string(params.State)
	if state != "read" && state != "archived" {
		writeError(w, http.StatusBadRequest, "invalid_state", "state: read o archived (le non lette non si eliminano in blocco).")
		return
	}
	f, ok := parseInboxFilter(w, params.Reason, params.Repo)
	if !ok {
		return
	}
	sc, ok := s.scopeFor(w, r, userID)
	if !ok {
		return
	}
	cond, _ := stateCond(state)
	where, args := inboxWhere(sc, f)
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM core.notifications WHERE id IN (SELECT n.id`+inboxFrom+where+cond+`)`, args...)
	if err != nil {
		writeIssueFailure(w, "eliminazione delle notifiche non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.DeletedNotifications{Deleted: int(tag.RowsAffected())})
}

// MarkAllNotificationsRead implementa POST /notifications/read-all.
func (s *apiServer) MarkAllNotificationsRead(w http.ResponseWriter, r *http.Request, params openapi.MarkAllNotificationsReadParams) {
	userID, ok := s.callerForInbox(w, r)
	if !ok {
		return
	}
	f, ok := parseInboxFilter(w, params.Reason, params.Repo)
	if !ok {
		return
	}
	sc, ok := s.scopeFor(w, r, userID)
	if !ok {
		return
	}
	where, args := inboxWhere(sc, f)
	args = append(args, s.now())
	tag, err := s.pool.Exec(r.Context(), fmt.Sprintf(`UPDATE core.notifications SET read_at = $%d WHERE id IN (SELECT n.id`, len(args))+
		inboxFrom+where+unreadCond+`)`, args...)
	if err != nil {
		writeIssueFailure(w, "aggiornamento delle notifiche non riuscito", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.MarkedNotifications{Marked: int(tag.RowsAffected())})
}
