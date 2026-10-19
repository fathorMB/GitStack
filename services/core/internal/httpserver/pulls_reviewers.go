package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Richiesta di review (PR12c, GIT-262): la chiedono, o la chiedono di nuovo,
// l'autore della PR e chi ha write; i revisori hanno almeno read sul repo.

func writeReviewerNoAccess(w http.ResponseWriter, msg string) {
	writeErrorDetails(w, http.StatusUnprocessableEntity, "reviewer_no_access", msg, map[string]any{"fields": map[string]any{"reviewers": msg}})
}

// RequestPullRequestReviewers implementa POST .../pulls/{number}/requested-reviewers.
func (s *apiServer) RequestPullRequestReviewers(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam) {
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
		writeError(w, http.StatusForbidden, "forbidden", "Chiedono la review l'autore della Pull Request e chi ha write.")
		return
	}
	var in openapi.RequestReviewersInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if len(in.Reviewers) == 0 || len(in.Reviewers) > 10 {
		writeFieldError(w, "reviewers", "reviewers: da 1 a 10 utenti per chiamata.")
		return
	}
	if code, msg := reviewRequestBlock(cur); code != "" {
		writeError(w, http.StatusConflict, code, msg)
		return
	}
	var ids []uuid.UUID
	var named []assignee
	gs := s.pullGitBestEffort(ctx, ia, cur)
	seen := map[string]bool{}
	for _, n := range in.Reviewers {
		key := strings.ToLower(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		o, err := s.repoIdentity.ResolveOwner(ctx, key)
		if errors.Is(err, identityclient.ErrNotFound) || (err == nil && o.Type != "user") {
			writeFieldError(w, "reviewers", fmt.Sprintf("reviewers: %q non è un utente.", n))
			return
		}
		var can bool
		if err == nil {
			can, err = s.repoIdentity.HasRole(ctx, o.ID, ia.repo.ID, "read")
		}
		if err != nil {
			writeIssueFailure(w, "verifica dei revisori non riuscita", fmt.Errorf("%w: %v", errUsersUnavailable, err))
			return
		}
		if o.ID == cur.AuthorID {
			writeFieldError(w, "reviewers", fmt.Sprintf("reviewers: %q è l'autore della Pull Request.", n))
			return
		}
		if !can {
			writeReviewerNoAccess(w, fmt.Sprintf("reviewers: %q non ha il permesso read sul repo.", n))
			return
		}
		ids = append(ids, o.ID)
		named = append(named, assignee{id: o.ID, name: o.Name})
	}

	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if code, msg := reviewRequestBlock(p); code != "" {
		writeError(w, http.StatusConflict, code, msg)
		return
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO core.pull_request_review_requests (pull_request_id, reviewer_id, requested_by)
			VALUES ($1, $2, $3)
			ON CONFLICT (pull_request_id, reviewer_id) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now()`,
			p.ID, id, ia.userID); err != nil {
			writeIssueFailure(w, "richiesta dei revisori non riuscita", err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := s.emitReviewRequested(ctx, tx, domainevents.PullRequestReviewRequested, ia, gs, number, named); err != nil {
		writeIssueFailure(w, "scrittura degli eventi non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della richiesta non riuscito", err)
		return
	}
	s.respondPull(w, ctx, http.StatusOK, ia, number, nil)
}

// reviewRequestBlock: una bozza non chiede review (PR6), una PR non aperta nemmeno.
func reviewRequestBlock(p prRow) (code, msg string) {
	if p.State != "open" {
		return "not_open", "La Pull Request non è aperta."
	}
	if p.Draft {
		return "draft", "Una bozza non chiede review: segnala prima «Ready for review»."
	}
	return "", ""
}

// RemovePullRequestReviewer implementa DELETE .../requested-reviewers/{username}.
func (s *apiServer) RemovePullRequestReviewer(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.PullNumberParam, username openapi.UsernameParam) {
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
		writeError(w, http.StatusForbidden, "forbidden", "Tolgono la richiesta l'autore della Pull Request e chi ha write.")
		return
	}
	gs := s.pullGitBestEffort(ctx, ia, cur)
	o, err := s.repoIdentity.ResolveOwner(ctx, strings.ToLower(username))
	if errors.Is(err, identityclient.ErrNotFound) || (err == nil && o.Type != "user") {
		writeError(w, http.StatusNotFound, "not_found", "La persona non è fra i revisori richiesti.")
		return
	}
	if err != nil {
		writeIssueFailure(w, "risoluzione del revisore non riuscita", fmt.Errorf("%w: %v", errUsersUnavailable, err))
		return
	}
	tx, p, ok := s.beginPullWrite(w, r, ia, number)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM core.pull_request_review_requests WHERE pull_request_id = $1 AND reviewer_id = $2`, p.ID, o.ID)
	if err != nil {
		writeIssueFailure(w, "rimozione della richiesta non riuscita", err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found", "La persona non è fra i revisori richiesti.")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE core.pull_requests SET updated_at = now() WHERE id = $1`, p.ID); err != nil {
		writeIssueFailure(w, "aggiornamento della Pull Request non riuscito", err)
		return
	}
	if err := s.emitReviewRequested(ctx, tx, domainevents.PullRequestReviewRequestRemoved, ia, gs, number, []assignee{{id: o.ID, name: o.Name}}); err != nil {
		writeIssueFailure(w, "scrittura degli eventi non riuscita", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeIssueFailure(w, "commit della rimozione non riuscito", err)
		return
	}
	s.respondPull(w, ctx, http.StatusOK, ia, number, nil)
}
