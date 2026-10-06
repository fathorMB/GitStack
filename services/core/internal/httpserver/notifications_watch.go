package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Watch del repo e iscrizione alle issues (M-06/E, GIT-133; regola C3).
// Participating è il default (nessuna riga); All e Ignore stanno in
// core.repo_watches. Serve `read` sul repo (404 se non si legge).

// GetRepoWatch implementa GET /repos/{owner}/{repo}/watch.
func (s *apiServer) GetRepoWatch(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	repoID, userID, ok := s.watchTarget(w, r, owner, name)
	if !ok {
		return
	}
	s.respondWatch(w, r, repoID, userID)
}

func (s *apiServer) respondWatch(w http.ResponseWriter, r *http.Request, repoID, userID uuid.UUID) {
	out := openapi.RepoWatch{Mode: openapi.RepoWatchMode("participating")}
	var mode string
	var updated time.Time
	err := s.pool.QueryRow(r.Context(), `SELECT mode, updated_at FROM core.repo_watches WHERE repo_id = $1 AND user_id = $2`, repoID, userID).Scan(&mode, &updated)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		writeIssueFailure(w, "lettura del Watch non riuscita", err)
		return
	default:
		out.Mode = openapi.RepoWatchMode(mode)
		out.UpdatedAt = &updated
	}
	writeJSON(w, http.StatusOK, out)
}

// watchTarget risolve il repo (404 se il chiamante non lo legge).
func (s *apiServer) watchTarget(w http.ResponseWriter, r *http.Request, owner, name string) (repoID, userID uuid.UUID, ok bool) {
	_, userID, ok = callerIdentity(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	if !s.inboxReady(w) {
		return uuid.Nil, uuid.Nil, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return repo.ID, userID, true
}

// SetRepoWatch implementa PUT /repos/{owner}/{repo}/watch.
func (s *apiServer) SetRepoWatch(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	repoID, userID, ok := s.watchTarget(w, r, owner, name)
	if !ok {
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	switch in.Mode {
	case "participating":
		// Il default non si conserva: come un Reset.
		if _, err := s.pool.Exec(r.Context(), `DELETE FROM core.repo_watches WHERE repo_id = $1 AND user_id = $2`, repoID, userID); err != nil {
			writeIssueFailure(w, "scrittura del Watch non riuscita", err)
			return
		}
	case "all", "ignore":
		if _, err := s.pool.Exec(r.Context(), `INSERT INTO core.repo_watches (repo_id, user_id, mode) VALUES ($1, $2, $3)
			ON CONFLICT (repo_id, user_id) DO UPDATE SET mode = EXCLUDED.mode, updated_at = now()`, repoID, userID, in.Mode); err != nil {
			writeIssueFailure(w, "scrittura del Watch non riuscita", err)
			return
		}
	default:
		writeFieldError(w, "mode", "mode: participating, all o ignore.")
		return
	}
	s.respondWatch(w, r, repoID, userID)
}

// ResetRepoWatch implementa DELETE /repos/{owner}/{repo}/watch.
func (s *apiServer) ResetRepoWatch(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	repoID, userID, ok := s.watchTarget(w, r, owner, name)
	if !ok {
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM core.repo_watches WHERE repo_id = $1 AND user_id = $2`, repoID, userID); err != nil {
		writeIssueFailure(w, "ripristino del Watch non riuscito", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// subscriptionState legge lo stato dell'iscrizione dell'utente alla issue.
// Senza riga valgono le regole automatiche già note alla richiesta: l'autore
// e gli assegnatari seguono anche prima che il motore le abbia scritte.
func (s *apiServer) subscriptionState(ctx context.Context, issueID, authorID, userID uuid.UUID) (openapi.IssueSubscription, error) {
	var subscribed bool
	var reason string
	err := s.pool.QueryRow(ctx, `SELECT subscribed, reason FROM core.issue_subscriptions WHERE issue_id = $1 AND user_id = $2`, issueID, userID).Scan(&subscribed, &reason)
	switch {
	case err == nil:
		if !subscribed {
			return openapi.IssueSubscription{Subscribed: false, Reason: openapi.IssueSubscriptionReasonNone}, nil
		}
		return openapi.IssueSubscription{Subscribed: true, Reason: openapi.IssueSubscriptionReason(reason)}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return openapi.IssueSubscription{}, err
	}
	if authorID == userID {
		return openapi.IssueSubscription{Subscribed: true, Reason: openapi.IssueSubscriptionReasonAuthor}, nil
	}
	var assigned bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.issue_assignees WHERE issue_id = $1 AND user_id = $2)`, issueID, userID).Scan(&assigned); err != nil {
		return openapi.IssueSubscription{}, err
	}
	if assigned {
		return openapi.IssueSubscription{Subscribed: true, Reason: openapi.IssueSubscriptionReasonAssignee}, nil
	}
	return openapi.IssueSubscription{Subscribed: false, Reason: openapi.IssueSubscriptionReasonNone}, nil
}

// subscriptionIssue risolve repo e issue leggibili (una nascosta è 404 per chi
// non è admin, I4).
func (s *apiServer) subscriptionIssue(w http.ResponseWriter, r *http.Request, owner, name string, number int64) (issueRow, issueAccess, bool) {
	ia, ok := s.issueAccessFor(w, r, owner, name)
	if !ok {
		return issueRow{}, ia, false
	}
	x, ok := s.issueFor(w, r.Context(), s.pool, ia, number, false)
	return x, ia, ok
}

// GetIssueSubscription implementa GET .../issues/{number}/subscription.
func (s *apiServer) GetIssueSubscription(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	x, ia, ok := s.subscriptionIssue(w, r, owner, name, number)
	if !ok {
		return
	}
	st, err := s.subscriptionState(r.Context(), x.ID, x.AuthorID, ia.userID)
	if err != nil {
		writeIssueFailure(w, "lettura dell'iscrizione non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// SubscribeIssue implementa PUT .../subscription: segue la issue (manual),
// anche dopo un Unsubscribe. Idempotente.
func (s *apiServer) SubscribeIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	x, ia, ok := s.subscriptionIssue(w, r, owner, name, number)
	if !ok {
		return
	}
	if _, err := s.pool.Exec(r.Context(), `INSERT INTO core.issue_subscriptions (issue_id, user_id, subscribed, reason) VALUES ($1, $2, true, 'manual')
		ON CONFLICT (issue_id, user_id) DO UPDATE SET subscribed = true, reason = 'manual', updated_at = now()`, x.ID, ia.userID); err != nil {
		writeIssueFailure(w, "iscrizione non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.IssueSubscription{Subscribed: true, Reason: openapi.IssueSubscriptionReasonManual})
}

// UnsubscribeIssue implementa DELETE .../subscription: da qui l'iscrizione
// automatica non riguarda più l'utente per questa issue (la riga resta con
// subscribed=false). Le menzioni dirette e le assegnazioni notificano
// comunque. Idempotente.
func (s *apiServer) UnsubscribeIssue(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, number openapi.IssueNumberParam) {
	x, ia, ok := s.subscriptionIssue(w, r, owner, name, number)
	if !ok {
		return
	}
	if _, err := s.pool.Exec(r.Context(), `INSERT INTO core.issue_subscriptions (issue_id, user_id, subscribed, reason) VALUES ($1, $2, false, 'manual')
		ON CONFLICT (issue_id, user_id) DO UPDATE SET subscribed = false, updated_at = now()`, x.ID, ia.userID); err != nil {
		writeIssueFailure(w, "disiscrizione non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.IssueSubscription{Subscribed: false, Reason: openapi.IssueSubscriptionReasonNone})
}
