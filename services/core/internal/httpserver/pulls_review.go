package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Review delle Pull Request (v1.1, GIT-262): Comment, Approve e Request
// changes (PR2, PR5). Approvare o chiedere modifiche vuole write e non vale
// sulla propria PR; l'approvazione di un agente si registra e si vede ma non
// conta (by_agent). Lo stato di review della PR sta in pullReviewState
// (pulls_model.go), che conta solo le persone.

var reviewStateToAPI = map[string]openapi.ReviewState{
	"comment":         openapi.ReviewStateCommented,
	"approve":         openapi.ReviewStateApproved,
	"request_changes": openapi.ReviewStateChangesRequested,
}

type reviewRow struct {
	ID           uuid.UUID
	AuthorID     uuid.UUID
	State        string
	Body         string
	Commit       string
	ByAgent      bool
	CreatedAt    time.Time
	CommentCount int
}

// reviewViews compone le review: stale e countsAsApproval (PR2, PR4). Conta
// come approvazione solo l'ultima review decisiva (approve o request_changes)
// di una persona, sulla punta attuale, non dell'autore e non di un agente.
func (s *apiServer) reviewViews(ctx context.Context, p prRow, all []reviewRow, head string) ([]openapi.PullRequestReview, error) {
	latest := map[uuid.UUID]uuid.UUID{}
	users := make([]uuid.UUID, 0, len(all))
	for _, rv := range all {
		users = append(users, rv.AuthorID)
		if rv.State == "approve" || rv.State == "request_changes" {
			latest[rv.AuthorID] = rv.ID
		}
	}
	dir, err := s.resolveUsers(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]openapi.PullRequestReview, 0, len(all))
	for _, rv := range all {
		stale := rv.State == "approve" && rv.Commit != head
		out = append(out, openapi.PullRequestReview{
			Id: openapi_types.UUID(rv.ID), Author: dir[rv.AuthorID], State: reviewStateToAPI[rv.State], Body: rv.Body,
			CommitSha: rv.Commit, Stale: stale, CommentCount: rv.CommentCount, SubmittedAt: rv.CreatedAt,
			CountsAsApproval: rv.State == "approve" && !stale && !rv.ByAgent && rv.AuthorID != p.AuthorID && latest[rv.AuthorID] == rv.ID,
		})
	}
	return out, nil
}

func (s *apiServer) pullReviews(ctx context.Context, q querier, p prRow) ([]reviewRow, error) {
	rows, err := q.Query(ctx, `SELECT r.id, r.author_id, r.state, r.body, r.commit_sha, r.by_agent, r.created_at,
		(SELECT count(*) FROM core.pull_request_threads t WHERE t.review_id = r.id)
		FROM core.pull_request_reviews r WHERE r.pull_request_id = $1 ORDER BY r.created_at, r.id`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reviewRow
	for rows.Next() {
		var rv reviewRow
		if err := rows.Scan(&rv.ID, &rv.AuthorID, &rv.State, &rv.Body, &rv.Commit, &rv.ByAgent, &rv.CreatedAt, &rv.CommentCount); err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

// ListPullRequestReviews implementa GET .../pulls/{number}/reviews.
func (s *apiServer) ListPullRequestReviews(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, params openapi.ListPullRequestReviewsParams) {
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
	if !ok {
		return
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
	all, err := s.pullReviews(ctx, s.pool, p)
	if err != nil {
		writeIssueFailure(w, "lettura delle review non riuscita", err)
		return
	}
	views, err := s.reviewViews(ctx, p, all, pullHead(branches, p))
	if err != nil {
		writeIssueFailure(w, "composizione delle review non riuscita", err)
		return
	}
	start := min((page-1)*perPage, len(views))
	end := min(start+perPage, len(views))
	writeJSON(w, http.StatusOK, openapi.PullRequestReviewList{Items: views[start:end], Page: page, PerPage: perPage, Total: len(views)})
}

// CreatePullRequestReview implementa POST .../pulls/{number}/reviews.
func (s *apiServer) CreatePullRequestReview(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return
	}
	ctx := r.Context()
	cur, ok := s.pullFor(w, ctx, s.pool, ia, number, false)
	if !ok || !s.pullMutable(w, ctx, ia) {
		return
	}
	var in openapi.CreatePullRequestReviewInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	var state string
	switch in.Event {
	case openapi.Comment:
		state = "comment"
	case openapi.Approve:
		state = "approve"
	case openapi.RequestChanges:
		state = "request_changes"
	default:
		writeFieldError(w, "event", "event: comment, approve o request_changes.")
		return
	}
	body := ""
	if in.Body != nil {
		body = *in.Body
	}
	if !textProblem(w, "body", body, true) {
		return
	}
	var comments []openapi.ReviewCommentInput
	if in.Comments != nil {
		comments = *in.Comments
	}
	if len(comments) > 100 {
		writeFieldError(w, "comments", "comments: al massimo 100.")
		return
	}
	for i, c := range comments {
		if !textProblem(w, fmt.Sprintf("comments[%d].body", i), c.Body, false) {
			return
		}
		if c.Path == "" {
			writeFieldError(w, fmt.Sprintf("comments[%d].path", i), fmt.Sprintf("comments[%d].path: obbligatorio.", i))
			return
		}
	}

	// PR2: approvare o chiedere modifiche vuole write e non vale sulla propria PR.
	if state != "comment" {
		if cur.AuthorID == ia.userID {
			writeError(w, http.StatusForbidden, "own_pull_request", "Non si approva né si chiedono modifiche alla propria Pull Request.")
			return
		}
		if !ia.write {
			writeError(w, http.StatusForbidden, "forbidden", "Approvare o chiedere modifiche richiede il permesso write sul repo.")
			return
		}
	}
	if cur.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "La Pull Request non è aperta.")
		return
	}
	if cur.Draft && state != "comment" {
		writeError(w, http.StatusConflict, "draft", "Una bozza si può commentare, ma non approvare né chiedere modifiche.")
		return
	}
	if state == "request_changes" && body == "" && len(comments) == 0 {
		writeFieldError(w, "body", "body: «Request changes» vuole un testo o almeno un commento.")
		return
	}
	if state == "comment" && body == "" && len(comments) == 0 {
		writeFieldError(w, "body", "body: la review non ha né testo né commenti.")
		return
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
	head := pullHead(branches, cur)
	if in.CommitSha != nil && *in.CommitSha != head {
		writeError(w, http.StatusConflict, "head_changed", "La punta della Pull Request è cambiata: aggiorna la pagina e rivedi i nuovi commit.")
		return
	}
	if len(comments) > 0 {
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
		for i, c := range comments {
			start := 0
			if c.StartLine != nil {
				start = *c.StartLine
			}
			if msg, notIn := lineProblem(cmp, c.Path, c.Side, start, c.Line); msg != "" {
				field := fmt.Sprintf("comments[%d]", i)
				if notIn {
					writeLineNotInDiff(w, field, field+": "+msg)
				} else {
					writeFieldError(w, field, field+": "+msg)
				}
				return
			}
		}
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
	if p.State != "open" {
		writeError(w, http.StatusConflict, "not_open", "La Pull Request non è aperta.")
		return
	}
	if p.Draft && state != "comment" {
		writeError(w, http.StatusConflict, "draft", "Una bozza si può commentare, ma non approvare né chiedere modifiche.")
		return
	}
	reviewID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_reviews (id, pull_request_id, author_id, state, body, commit_sha, by_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, clock_timestamp())`, reviewID, p.ID, ia.userID, state, body, head, agent); err != nil {
		writeIssueFailure(w, "scrittura della review non riuscita", err)
		return
	}
	for _, c := range comments {
		start := 0
		if c.StartLine != nil {
			start = *c.StartLine
		}
		if _, err := insertThread(ctx, tx, p, &reviewID, head, c.Path, c.Side, start, c.Line, ia.userID, agent, c.Body); err != nil {
			writeIssueFailure(w, "scrittura dei commenti non riuscita", err)
			return
		}
	}
	// Chi ha recensito non è più un revisore in attesa (PR12).
	if _, err := tx.Exec(ctx, `DELETE FROM core.pull_request_review_requests WHERE pull_request_id = $1 AND reviewer_id = $2`, p.ID, ia.userID); err != nil {
		writeIssueFailure(w, "aggiornamento dei revisori non riuscito", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della review non riuscito", err)
		return
	}
	all, err := s.pullReviews(ctx, s.pool, p)
	if err != nil {
		writeIssueFailure(w, "lettura delle review non riuscita", err)
		return
	}
	views, err := s.reviewViews(ctx, p, all, head)
	if err != nil {
		writeIssueFailure(w, "composizione delle review non riuscita", err)
		return
	}
	for i, rv := range all {
		if rv.ID == reviewID {
			w.Header().Set("Location", pullLocation(ia, p.Number)+"/reviews")
			writeJSON(w, http.StatusCreated, views[i])
			return
		}
	}
	writeIssueFailure(w, "review non trovata dopo la scrittura", fmt.Errorf("review %s assente", reviewID))
}
