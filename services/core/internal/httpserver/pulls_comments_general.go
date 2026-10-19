package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Commenti generali ed eliminazione (v1.1, GIT-300, PR5). Un commento
// generale ha thread_id NULL (migrazione 0021); uno eliminato che ha ancora
// risposte resta come segnaposto (deleted, testo vuoto).

// createGeneralPullComment scrive un commento della conversazione, senza path:
// serve `read` sul repo (come per le issue) e vale anche su una PR chiusa o
// unita. La timeline eventi della PR non ha un tipo adatto: non se ne aggiunge.
func (s *apiServer) createGeneralPullComment(w http.ResponseWriter, r *http.Request, ia issueAccess, number int64, in openapi.CreatePullRequestCommentInput) {
	ctx := r.Context()
	if in.Side != nil || in.Line != nil || in.StartLine != nil || in.CommitSha != nil {
		writeFieldError(w, "path", "side, line, startLine e commitSha valgono solo con path.")
		return
	}
	agent, err := s.callerIsAgent(ctx, ia.userID)
	if err != nil {
		writeIssueFailure(w, "verifica del tipo di account non riuscita", err)
		return
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body, by_agent, created_at, updated_at)
		VALUES ($1, NULL, $2, $3, $4, $5, clock_timestamp(), clock_timestamp())`, id, p.ID, ia.userID, in.Body, agent); err != nil {
		writeIssueFailure(w, "scrittura del commento non riuscita", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit del commento non riuscito", err)
		return
	}
	s.respondPullComment(w, r, ia, id, http.StatusCreated)
}

// DeletePullRequestComment implementa DELETE .../comments/{commentId}: lo
// fanno l'autore e chi ha admin sul repo. Un commento che apre una
// conversazione con risposte resta come segnaposto e le risposte restano
// (PR5); senza risposte sparisce con la conversazione. Quando sparisce
// l'ultima risposta di un segnaposto, sparisce anche lui.
func (s *apiServer) DeletePullRequestComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, commentID openapi.PullCommentIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.pullFor(w, ctx, s.pool, ia, number, false); !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := uuid.UUID(commentID)
	var thread *uuid.UUID
	var author uuid.UUID
	err := tx.QueryRow(ctx, `SELECT thread_id, author_id FROM core.pull_request_comments
		WHERE id = $1 AND pull_request_id = $2 AND NOT deleted FOR UPDATE`, id, p.ID).Scan(&thread, &author)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Commento non trovato.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	if author != ia.userID && !ia.admin {
		writeError(w, http.StatusForbidden, "forbidden", "Elimina un commento l'autore o chi ha admin.")
		return
	}
	if err := removePullComment(ctx, tx, id, thread); err != nil {
		writeIssueFailure(w, "eliminazione del commento non riuscita", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit dell'eliminazione non riuscito", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removePullComment applica la regola del segnaposto (PR5).
func removePullComment(ctx context.Context, tx pgx.Tx, id uuid.UUID, thread *uuid.UUID) error {
	if thread == nil {
		_, err := tx.Exec(ctx, `DELETE FROM core.pull_request_comments WHERE id = $1`, id)
		return err
	}
	if *thread == id { // primo commento della conversazione
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_comments WHERE thread_id = $1 AND id <> $1`, id).Scan(&others); err != nil {
			return err
		}
		if others == 0 { // la conversazione sparisce con il suo unico commento
			_, err := tx.Exec(ctx, `DELETE FROM core.pull_request_threads WHERE id = $1`, id)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE core.pull_request_comments SET body = '', deleted = true, updated_at = now() WHERE id = $1`, id)
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.pull_request_comments WHERE id = $1`, id); err != nil {
		return err
	}
	// Segnaposto rimasto senza risposte: non serve più.
	_, err := tx.Exec(ctx, `DELETE FROM core.pull_request_threads t WHERE t.id = $1
		AND EXISTS (SELECT 1 FROM core.pull_request_comments c WHERE c.id = t.id AND c.deleted)
		AND NOT EXISTS (SELECT 1 FROM core.pull_request_comments c WHERE c.thread_id = t.id AND c.id <> t.id)`, *thread)
	return err
}
