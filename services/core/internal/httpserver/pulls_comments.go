package httpserver

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Commenti sulle righe e risposte (v1.1, GIT-262, PR5). I commenti generali
// della conversazione (senza path) e l'eliminazione con segnaposto non hanno
// ancora una tabella (core.pull_request_comments è legata a una
// conversazione): restano 501 finché il modello non cresce.

// ListPullRequestComments implementa GET .../pulls/{number}/comments: elenco
// piatto dei commenti sulle righe, dal meno recente.
func (s *apiServer) ListPullRequestComments(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestCommentsParams) {
	page, perPage, ok := pagination(w, params.Page, params.PerPage)
	if !ok {
		return
	}
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	p, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.refreshOutdated(w, ctx, ia, p) {
		return
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_comments c
		JOIN core.pull_request_threads t ON t.id = c.thread_id WHERE t.pull_request_id = $1`, p.ID).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio dei commenti non riuscito", err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.thread_id, c.author_id, c.body, c.edited, c.created_at, c.updated_at,
		`+threadCols+`, c.id = t.id
		FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
		WHERE t.pull_request_id = $1 ORDER BY c.created_at, c.id LIMIT $2 OFFSET $3`, p.ID, perPage, (page-1)*perPage)
	if err != nil {
		writeIssueFailure(w, "lettura dei commenti non riuscita", err)
		return
	}
	type item struct {
		c     prCommentRow
		t     threadRow
		first bool
	}
	var items []item
	var users []uuid.UUID
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.c.ID, &it.c.ThreadID, &it.c.AuthorID, &it.c.Body, &it.c.Edited, &it.c.CreatedAt, &it.c.UpdatedAt,
			&it.t.ID, &it.t.ReviewID, &it.t.Commit, &it.t.Path, &it.t.Side, &it.t.StartLine, &it.t.Line, &it.t.Outdated,
			&it.t.Resolved, &it.t.ResolvedBy, &it.t.ResolvedAt, &it.t.CreatedAt, &it.first); err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura dei commenti non riuscita", err)
			return
		}
		users = append(users, it.c.AuthorID)
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura dei commenti non riuscita", err)
		return
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		writeIssueFailure(w, "risoluzione degli utenti non riuscita", err)
		return
	}
	out := make([]openapi.PullRequestComment, 0, len(items))
	for _, it := range items {
		out = append(out, commentView(it.c, it.t, it.first, dir))
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestCommentList{Items: out, Page: page, PerPage: perPage, Total: total})
}

// CreatePullRequestComment implementa POST .../pulls/{number}/comments: un
// commento su una riga o su un gruppo di righe (apre una conversazione) o una
// risposta in una conversazione esistente.
func (s *apiServer) CreatePullRequestComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	var in openapi.CreatePullRequestCommentInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !textProblem(w, "body", in.Body, false) {
		return
	}
	if in.InReplyTo != nil && in.Path != nil {
		writeFieldError(w, "inReplyTo", "inReplyTo: esclude path.")
		return
	}
	if in.InReplyTo == nil && in.Path == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "I commenti generali della Pull Request non sono ancora disponibili: servono path, side, line e commitSha, oppure inReplyTo.")
		return
	}
	if cur.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "I commenti sulle righe si scrivono su una Pull Request aperta.")
		return
	}
	agent, err := s.callerIsAgent(ctx, ia.userID)
	if err != nil {
		writeIssueFailure(w, "verifica del tipo di account non riuscita", err)
		return
	}

	var (
		head, path string
		side       openapi.DiffSide
		start, ln  int
	)
	if in.Path != nil {
		if in.Side == nil || in.Line == nil || in.CommitSha == nil {
			writeFieldError(w, "path", "path: servono anche side, line e commitSha.")
			return
		}
		path, side, ln = *in.Path, *in.Side, *in.Line
		if in.StartLine != nil {
			start = *in.StartLine
		}
		g, ok := s.pullGit(w, ia)
		if !ok {
			return
		}
		branches, err := g.branches(ctx)
		if err != nil {
			gitFailure(w, err)
			return
		}
		head = pullHead(branches, cur)
		if *in.CommitSha != head {
			writeError(w, http.StatusConflict, "head_changed", "La punta della Pull Request è cambiata: aggiorna la pagina e rivedi i nuovi commit.")
			return
		}
		base, hd, _, ok := cur.refs(branches)
		if !ok {
			writeError(w, http.StatusConflict, "branch_missing", "Il branch di origine non esiste più.")
			return
		}
		cmp, err := g.compare(ctx, base, hd, false)
		if err != nil {
			gitFailure(w, err)
			return
		}
		if msg, notIn := lineProblem(cmp, path, side, start, ln); msg != "" {
			if notIn {
				writeLineNotInDiff(w, "line", "line: "+msg)
			} else {
				writeFieldError(w, "line", "line: "+msg)
			}
			return
		}
	}

	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "I commenti sulle righe si scrivono su una Pull Request aperta.")
		return
	}
	var threadID, commentID uuid.UUID
	if in.InReplyTo != nil {
		err := tx.QueryRow(ctx, `SELECT t.id FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
			WHERE c.id = $1 AND t.pull_request_id = $2`, uuid.UUID(*in.InReplyTo), p.ID).Scan(&threadID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "Commento non trovato.")
			return
		}
		if err != nil {
			writeIssueFailure(w, "lettura del commento non riuscita", err)
			return
		}
		commentID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_comments (id, thread_id, author_id, body, by_agent, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, clock_timestamp(), clock_timestamp())`, commentID, threadID, ia.userID, in.Body, agent); err != nil {
			writeIssueFailure(w, "scrittura del commento non riuscita", err)
			return
		}
	} else {
		if threadID, err = insertThread(ctx, tx, p, nil, head, path, side, start, ln, ia.userID, agent, in.Body); err != nil {
			writeIssueFailure(w, "scrittura del commento non riuscita", err)
			return
		}
		commentID = threadID
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit del commento non riuscito", err)
		return
	}
	s.respondPullComment(w, r, ia, commentID, http.StatusCreated)
}

// respondPullComment rilegge un commento e lo scrive nella risposta.
func (s *apiServer) respondPullComment(w http.ResponseWriter, r *http.Request, ia issueAccess, id uuid.UUID, status int) {
	ctx := r.Context()
	var c prCommentRow
	var t threadRow
	var first bool
	err := s.pool.QueryRow(ctx, `SELECT c.id, c.thread_id, c.author_id, c.body, c.edited, c.created_at, c.updated_at,
		`+threadCols+`, c.id = t.id
		FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id WHERE c.id = $1`, id).
		Scan(&c.ID, &c.ThreadID, &c.AuthorID, &c.Body, &c.Edited, &c.CreatedAt, &c.UpdatedAt,
			&t.ID, &t.ReviewID, &t.Commit, &t.Path, &t.Side, &t.StartLine, &t.Line, &t.Outdated,
			&t.Resolved, &t.ResolvedBy, &t.ResolvedAt, &t.CreatedAt, &first)
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	dir, err := s.resolveUsers(ctx, []uuid.UUID{c.AuthorID})
	if err != nil {
		writeIssueFailure(w, "risoluzione degli utenti non riuscita", err)
		return
	}
	writeJSON(w, status, commentView(c, t, first, dir))
}

// UpdatePullRequestComment implementa PATCH .../comments/{commentId}: solo l'autore.
func (s *apiServer) UpdatePullRequestComment(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, commentID openapi.PullCommentIdParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.pullFor(w, ctx, s.pool, ia, number, false); !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	var in openapi.UpdatePullRequestCommentInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !textProblem(w, "body", in.Body, false) {
		return
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var author uuid.UUID
	err := tx.QueryRow(ctx, `SELECT c.author_id FROM core.pull_request_comments c JOIN core.pull_request_threads t ON t.id = c.thread_id
		WHERE c.id = $1 AND t.pull_request_id = $2 FOR UPDATE OF c`, uuid.UUID(commentID), p.ID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Commento non trovato.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura del commento non riuscita", err)
		return
	}
	if author != ia.userID {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'autore modifica il proprio commento.")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_request_comments SET body = $2, edited = true, updated_at = now() WHERE id = $1`, uuid.UUID(commentID), in.Body); err != nil {
		writeIssueFailure(w, "modifica del commento non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della modifica non riuscito", err)
		return
	}
	s.respondPullComment(w, r, ia, uuid.UUID(commentID), http.StatusOK)
}
