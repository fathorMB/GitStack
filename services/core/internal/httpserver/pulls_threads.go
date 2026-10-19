package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Conversazioni sulle righe del diff (v1.1, GIT-262, regola PR5): righe
// valide, commenti «outdated» dopo nuovi commit, lettura e risoluzione.
// Una conversazione ha l'id del suo primo commento (come dice il contratto).

const maxPullCommentRunes = 65536

var hunkRe = regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// diffHunk sono gli intervalli di righe di un hunk, sul lato vecchio e nuovo.
type diffHunk struct{ oldStart, oldLen, newStart, newLen int }

func parseHunks(patch string) []diffHunk {
	num := func(s string) int {
		if s == "" {
			return 1
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	var out []diffHunk
	for _, m := range hunkRe.FindAllStringSubmatch(patch, -1) {
		out = append(out, diffHunk{oldStart: num(m[1]), oldLen: num(m[2]), newStart: num(m[3]), newLen: num(m[4])})
	}
	return out
}

// span ritorna il primo e l'ultimo numero di riga dell'hunk sul lato dato
// (ok=false se l'hunk non ha righe su quel lato).
func (h diffHunk) span(newSide bool) (from, to int, ok bool) {
	start, n := h.oldStart, h.oldLen
	if newSide {
		start, n = h.newStart, h.newLen
	}
	if n <= 0 {
		return 0, 0, false
	}
	return start, start + n - 1, true
}

// dbSide traduce il lato del contratto (old/new) in quello dello schema.
func dbSide(s openapi.DiffSide) string {
	if s == openapi.Old {
		return "left"
	}
	return "right"
}

func apiSide(s string) openapi.DiffSide {
	if s == "left" {
		return openapi.Old
	}
	return openapi.New
}

// lineProblem controlla che le righe stiano nel diff della PR: un messaggio
// vuoto vuol dire valide. start=0 per una riga sola.
func lineProblem(c pullCompare, path string, side openapi.DiffSide, start, line int) (msg string, notInDiff bool) {
	if side != openapi.Old && side != openapi.New {
		return "side: old o new.", false
	}
	if line < 1 || start < 0 {
		return "line: da 1 in su.", false
	}
	if start > 0 && start >= line {
		return "startLine: deve essere minore di line.", false
	}
	for _, f := range c.Files {
		match := f.Path == path || (side == openapi.Old && f.OldPath != nil && *f.OldPath == path)
		if !match {
			continue
		}
		if f.Patch == nil {
			break
		}
		from := line
		if start > 0 {
			from = start
		}
		for _, h := range parseHunks(*f.Patch) {
			if a, b, ok := h.span(side == openapi.New); ok && from >= a && line <= b {
				return "", false
			}
		}
		break
	}
	return fmt.Sprintf("le righe di %s non stanno nel diff della Pull Request.", path), true
}

func writeLineNotInDiff(w http.ResponseWriter, field, msg string) {
	writeErrorDetails(w, http.StatusUnprocessableEntity, "line_not_in_diff", msg, map[string]any{"fields": map[string]any{field: msg}})
}

// pullHead è la punta corrente della PR: il branch vivo se è aperta, altrimenti lo sha congelato.
func pullHead(branches map[string]string, p prRow) string {
	if p.State == "open" {
		if s, ok := branches[p.SourceBranch]; ok {
			return s
		}
	}
	return sha40(p.HeadSha)
}

// callerIsAgent: l'account del chiamante è un agente (PR2).
func (s *apiServer) callerIsAgent(ctx context.Context, id uuid.UUID) (bool, error) {
	dir, err := s.resolveUsers(ctx, []uuid.UUID{id})
	if err != nil {
		return false, err
	}
	return string(dir[id].Kind) == "agent", nil
}

// textProblem valida un testo: 413 oltre il limite, 422 se non è testo.
func textProblem(w http.ResponseWriter, field, text string, allowEmpty bool) bool {
	if utf8.RuneCountInString(text) > maxPullCommentRunes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Il testo supera 65536 caratteri.")
		return false
	}
	if strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		writeFieldError(w, field, field+": testo non valido.")
		return false
	}
	if !allowEmpty && strings.TrimSpace(text) == "" {
		writeFieldError(w, field, field+": obbligatorio.")
		return false
	}
	return true
}

type threadRow struct {
	ID         uuid.UUID
	ReviewID   *uuid.UUID
	Commit     string
	Path       string
	Side       string
	StartLine  *int
	Line       int
	Outdated   bool
	Resolved   bool
	ResolvedBy *uuid.UUID
	ResolvedAt *time.Time
	CreatedAt  time.Time
}

const threadCols = `t.id, t.review_id, t.commit_sha, t.path, t.side, t.start_line, t.line, t.outdated, t.resolved,
	t.resolved_by, t.resolved_at, t.created_at`

func scanThread(row pgx.Row) (threadRow, error) {
	var t threadRow
	err := row.Scan(&t.ID, &t.ReviewID, &t.Commit, &t.Path, &t.Side, &t.StartLine, &t.Line, &t.Outdated, &t.Resolved,
		&t.ResolvedBy, &t.ResolvedAt, &t.CreatedAt)
	return t, err
}

type prCommentRow struct {
	ID        uuid.UUID
	ThreadID  *uuid.UUID // nil: commento generale della conversazione della PR
	AuthorID  uuid.UUID
	Body      string
	Edited    bool
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func commentView(c prCommentRow, t threadRow, first bool, dir userDirectory) openapi.PullRequestComment {
	out := openapi.PullRequestComment{
		Id: openapi_types.UUID(c.ID), Author: dir[c.AuthorID], Body: c.Body, Edited: c.Edited, Deleted: c.Deleted,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.ThreadID == nil {
		return out
	}
	tid := openapi_types.UUID(t.ID)
	side := apiSide(t.Side)
	commit, path, line := t.Commit, t.Path, t.Line
	out.Outdated = t.Outdated
	out.ThreadId, out.Path, out.Side, out.Line, out.StartLine, out.CommitSha = &tid, &path, &side, &line, t.StartLine, &commit
	if first && t.ReviewID != nil {
		rid := openapi_types.UUID(*t.ReviewID)
		out.ReviewId = &rid
	}
	return out
}

// threadViews compone le conversazioni con i loro commenti.
func (s *apiServer) threadViews(ctx context.Context, q querier, rows []threadRow) ([]openapi.PullRequestThread, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, t := range rows {
		ids[i] = t.ID
	}
	cr, err := q.Query(ctx, `SELECT id, thread_id, author_id, body, edited, deleted, created_at, updated_at
		FROM core.pull_request_comments WHERE thread_id = ANY($1) ORDER BY created_at, id`, ids)
	if err != nil {
		return nil, err
	}
	by := map[uuid.UUID][]prCommentRow{}
	users := []uuid.UUID{}
	for cr.Next() {
		var c prCommentRow
		if err := cr.Scan(&c.ID, &c.ThreadID, &c.AuthorID, &c.Body, &c.Edited, &c.Deleted, &c.CreatedAt, &c.UpdatedAt); err != nil {
			cr.Close()
			return nil, err
		}
		by[*c.ThreadID] = append(by[*c.ThreadID], c)
		users = append(users, c.AuthorID)
	}
	cr.Close()
	if err := cr.Err(); err != nil {
		return nil, err
	}
	for _, t := range rows {
		if t.ResolvedBy != nil {
			users = append(users, *t.ResolvedBy)
		}
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.PullRequestThread, 0, len(rows))
	for _, t := range rows {
		th := openapi.PullRequestThread{
			Id: openapi_types.UUID(t.ID), Path: t.Path, Side: apiSide(t.Side), Line: t.Line, StartLine: t.StartLine,
			Resolved: t.Resolved, ResolvedAt: t.ResolvedAt, Outdated: t.Outdated, Comments: []openapi.PullRequestComment{},
		}
		if t.ResolvedBy != nil {
			u := dir[*t.ResolvedBy]
			th.ResolvedBy = &u
		}
		for i, c := range by[t.ID] {
			th.Comments = append(th.Comments, commentView(c, t, i == 0, dir))
		}
		out = append(out, th)
	}
	return out, nil
}

// syncOutdated marca outdated le conversazioni le cui righe sono cambiate dopo
// il commento (PR5). Lo stato è «appiccicoso»: una volta outdated resta tale.
// Si confrontano il commit del commento e la punta attuale; solo il lato
// nuovo può cambiare con i commit del branch di origine.
func (s *apiServer) syncOutdated(ctx context.Context, g pullGit, p prRow, branches map[string]string) error {
	head := pullHead(branches, p)
	rows, err := s.pool.Query(ctx, `SELECT id, commit_sha, path, start_line, line FROM core.pull_request_threads
		WHERE pull_request_id = $1 AND NOT outdated AND side = 'right' AND commit_sha <> $2`, p.ID, head)
	if err != nil {
		return err
	}
	type th struct {
		id           uuid.UUID
		path         string
		start, lineN int
	}
	byCommit := map[string][]th{}
	for rows.Next() {
		var t th
		var commit string
		var start *int
		if err := rows.Scan(&t.id, &commit, &t.path, &start, &t.lineN); err != nil {
			rows.Close()
			return err
		}
		t.start = t.lineN
		if start != nil {
			t.start = *start
		}
		byCommit[commit] = append(byCommit[commit], t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var stale []uuid.UUID
	for commit, ths := range byCommit {
		c, err := g.compare(ctx, commit, head, false)
		if err != nil {
			var ae *gitclient.APIError
			if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
				// il commit commentato non esiste più (riscrittura della storia)
				for _, t := range ths {
					stale = append(stale, t.id)
				}
				continue
			}
			slog.Default().Warn("confronto per i commenti outdated non riuscito", "pr", p.Number, "err", err)
			continue
		}
		for _, t := range ths {
			if threadChanged(c, t.path, t.start, t.lineN) {
				stale = append(stale, t.id)
			}
		}
	}
	if len(stale) == 0 {
		return nil
	}
	_, err = s.pool.Exec(ctx, `UPDATE core.pull_request_threads SET outdated = true WHERE id = ANY($1)`, stale)
	return err
}

// threadChanged: il confronto fra il commit commentato e la punta tocca le righe?
func threadChanged(c pullCompare, path string, start, line int) bool {
	for _, f := range c.Files {
		if f.Path != path && (f.OldPath == nil || *f.OldPath != path) {
			continue
		}
		if f.Status == openapi.FileDiffStatusDeleted || f.Path != path || f.Patch == nil {
			return true
		}
		for _, h := range parseHunks(*f.Patch) {
			if a, b, ok := h.span(false); ok && a <= line && b >= start {
				return true
			}
		}
		return false
	}
	return false
}

// ListPullRequestThreads implementa GET .../pulls/{number}/threads.
func (s *apiServer) ListPullRequestThreads(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestThreadsParams) {
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
	cond, args := "t.pull_request_id = $1", []any{p.ID}
	if params.Resolved != nil {
		args = append(args, *params.Resolved)
		cond += fmt.Sprintf(" AND t.resolved = $%d", len(args))
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.pull_request_threads t WHERE `+cond, args...).Scan(&total); err != nil {
		writeIssueFailure(w, "conteggio delle conversazioni non riuscito", err)
		return
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT `+threadCols+` FROM core.pull_request_threads t WHERE %s
		ORDER BY t.created_at, t.id LIMIT $%d OFFSET $%d`, cond, len(args)-1, len(args)), args...)
	if err != nil {
		writeIssueFailure(w, "lettura delle conversazioni non riuscita", err)
		return
	}
	var ths []threadRow
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			writeIssueFailure(w, "lettura delle conversazioni non riuscita", err)
			return
		}
		ths = append(ths, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeIssueFailure(w, "lettura delle conversazioni non riuscita", err)
		return
	}
	items, err := s.threadViews(ctx, s.pool, ths)
	if err != nil {
		writeIssueFailure(w, "composizione delle conversazioni non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.PullRequestThreadList{Items: items, Page: page, PerPage: perPage, Total: total})
}

// refreshOutdated rilegge i branch e aggiorna gli outdated prima di una lettura.
func (s *apiServer) refreshOutdated(w http.ResponseWriter, ctx context.Context, ia issueAccess, p prRow) bool {
	g, ok := s.pullGit(w, ia)
	if !ok {
		return false
	}
	branches, err := g.branches(ctx)
	if err != nil {
		gitFailure(w, err)
		return false
	}
	if err := s.syncOutdated(ctx, g, p, branches); err != nil {
		writeIssueFailure(w, "aggiornamento dei commenti outdated non riuscito", err)
		return false
	}
	return true
}

// ResolvePullRequestThread implementa POST .../threads/{threadId}/resolve.
func (s *apiServer) ResolvePullRequestThread(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, threadID openapi.PullThreadIdParam) {
	s.setThreadResolved(w, r, owner, name, number, uuid.UUID(threadID), true)
}

// UnresolvePullRequestThread implementa POST .../threads/{threadId}/unresolve.
func (s *apiServer) UnresolvePullRequestThread(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, threadID openapi.PullThreadIdParam) {
	s.setThreadResolved(w, r, owner, name, number, uuid.UUID(threadID), false)
}

// setThreadResolved: risolvono e riaprono l'autore della PR e chi ha write (PR5); idempotente.
func (s *apiServer) setThreadResolved(w http.ResponseWriter, r *http.Request, owner, name string, number int64, threadID uuid.UUID, resolve bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	if !ia.canManagePull(cur) {
		writeError(w, http.StatusForbidden, "forbidden", "Risolve le conversazioni l'autore della Pull Request o chi ha write.")
		return
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := scanThread(tx.QueryRow(ctx, `SELECT `+threadCols+` FROM core.pull_request_threads t
		WHERE t.id = $1 AND t.pull_request_id = $2 FOR UPDATE`, threadID, p.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Conversazione non trovata.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "lettura della conversazione non riuscita", err)
		return
	}
	if t.Resolved != resolve {
		if resolve {
			_, err = tx.Exec(ctx, `UPDATE core.pull_request_threads SET resolved = true, resolved_by = $2, resolved_at = now() WHERE id = $1`, t.ID, ia.userID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE core.pull_request_threads SET resolved = false, resolved_by = NULL, resolved_at = NULL WHERE id = $1`, t.ID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID)
		}
		if err != nil {
			writeIssueFailure(w, "modifica della conversazione non riuscita", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della conversazione non riuscito", err)
		return
	}
	t, err = scanThread(s.pool.QueryRow(ctx, `SELECT `+threadCols+` FROM core.pull_request_threads t WHERE t.id = $1`, threadID))
	if err != nil {
		writeIssueFailure(w, "lettura della conversazione non riuscita", err)
		return
	}
	views, err := s.threadViews(ctx, s.pool, []threadRow{t})
	if err != nil {
		writeIssueFailure(w, "composizione della conversazione non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, views[0])
}

// insertThread scrive una conversazione con il suo primo commento: l'id del
// commento è quello della conversazione.
func insertThread(ctx context.Context, q querier, p prRow, reviewID *uuid.UUID, commit, path string, side openapi.DiffSide, start, line int, author uuid.UUID, byAgent bool, body string) (uuid.UUID, error) {
	id := uuid.New()
	var startPtr *int
	if start > 0 {
		startPtr = &start
	}
	if _, err := q.Exec(ctx, `INSERT INTO core.pull_request_threads
		(id, pull_request_id, review_id, commit_sha, path, side, start_line, line, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, clock_timestamp())`,
		id, p.ID, reviewID, commit, path, dbSide(side), startPtr, line); err != nil {
		return uuid.Nil, err
	}
	_, err := q.Exec(ctx, `INSERT INTO core.pull_request_comments (id, thread_id, pull_request_id, author_id, body, by_agent, created_at, updated_at)
		VALUES ($1, $1, $2, $3, $4, $5, clock_timestamp(), clock_timestamp())`, id, p.ID, author, body, byAgent)
	return id, err
}
