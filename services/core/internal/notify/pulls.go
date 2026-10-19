package notify

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Notifiche delle Pull Request (v1.1, GIT-265, PR11). Stesse regole delle
// issues (C3-C8): chi segue la PR (autore, revisori, chi commenta, chi è
// menzionato, chi fa Subscribe) riceve le attività, chi ha Watch `all` sul
// repo riceve le PR nuove, i commenti e le review, mai l'attore, mai chi ha
// Watch `ignore`, mai chi non legge il repo. In più:
//   - review_requested: il revisore richiesto entra fra chi segue la PR e
//     riceve una notifica col motivo `review_requested` (anche dopo un
//     Unsubscribe); email attiva di default come le menzioni (C5).
//   - conflicts e checks_failed: solo l'autore, sulla propria PR.

// ReasonReviewRequested: all'utente è stata chiesta la review di una PR.
const ReasonReviewRequested = "review_requested"

func init() { priority[ReasonReviewRequested] = 7 }

// isPullEvent dice se il motore gestisce l'evento come evento di PR.
func isPullEvent(name string) bool {
	switch name {
	case domainevents.PullRequestOpened, domainevents.PullRequestEdited, domainevents.PullRequestClosed,
		domainevents.PullRequestReopened, domainevents.PullRequestMerged, domainevents.PullRequestReadyForReview,
		domainevents.PullRequestReviewRequested, domainevents.PullRequestConflicts, domainevents.PullRequestChecksFailed,
		domainevents.PullRequestReviewSubmitted, domainevents.PullRequestCommentCreated:
		return true
	}
	return false
}

func (e *Engine) handlePull(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	var p domainevents.PullPayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return &badEventError{err}
	}
	o := occurrence{name: ev.name}
	var err error
	if o.repoID, err = uuid.Parse(p.Repo.ID); err != nil {
		return &badEventError{err}
	}
	if o.pullID, err = uuid.Parse(p.Pull.ID); err != nil {
		return &badEventError{err}
	}
	if o.authorID, err = uuid.Parse(p.Pull.AuthorID); err != nil {
		return &badEventError{err}
	}
	if p.Actor != nil && p.Actor.ID != "" {
		if o.actor, err = uuid.Parse(p.Actor.ID); err != nil {
			return &badEventError{err}
		}
	}
	state := p.Pull.State
	if state == "merged" {
		state = "closed" // NotificationIssue.state: open | closed
	}
	o.data = map[string]any{"number": p.Pull.Number, "title": p.Pull.Title, "state": state, "pullRequest": true}
	if p.Pull.State == "merged" {
		o.data["merged"] = true
	}
	switch ev.name {
	case domainevents.PullRequestOpened:
		if err := tx.QueryRow(ctx, `SELECT body FROM core.pull_requests WHERE id = $1`, o.pullID).Scan(&o.text); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		o.draft = p.Pull.Draft
	case domainevents.PullRequestEdited:
		if p.Changes == nil || p.Changes.Body == nil {
			return nil // il titolo e la destinazione non citano nessuno
		}
		if err := tx.QueryRow(ctx, `SELECT body FROM core.pull_requests WHERE id = $1`, o.pullID).Scan(&o.text); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		o.prevText = p.Changes.Body.From
	case domainevents.PullRequestReviewRequested:
		if p.RequestedReviewer == nil {
			return &badEventError{errors.New("pull_request.review_requested senza revisore")}
		}
		if o.assignee, err = uuid.Parse(p.RequestedReviewer.ID); err != nil {
			return &badEventError{err}
		}
	case domainevents.PullRequestReviewSubmitted:
		if p.Review == nil {
			return &badEventError{errors.New("pull_request_review.submitted senza review")}
		}
		o.text = p.Review.Body
		o.data["review"] = map[string]any{"id": p.Review.ID, "state": p.Review.State}
	case domainevents.PullRequestCommentCreated:
		if p.Comment == nil {
			return &badEventError{errors.New("pull_request_comment.created senza commento")}
		}
		o.text = p.Comment.Body
		o.data["commentId"] = p.Comment.ID
	case domainevents.PullRequestChecksFailed:
		o.data["contexts"] = p.Contexts
	case domainevents.PullRequestConflicts:
		o.data["files"] = p.Files
	}
	return e.deliverPull(ctx, tx, o)
}

// deliverPull decide i destinatari di un evento di PR e scrive le notifiche.
func (e *Engine) deliverPull(ctx context.Context, tx pgx.Tx, o occurrence) error {
	o.access = map[uuid.UUID]bool{}
	cands := map[uuid.UUID]string{}
	add := func(u uuid.UUID, reason string) {
		if u == uuid.Nil || u == o.actor { // C3: mai per le proprie azioni
			return
		}
		if priority[reason] > priority[cands[u]] {
			cands[u] = reason
		}
	}
	autoSub := func(u uuid.UUID, reason string) error {
		_, err := tx.Exec(ctx, `INSERT INTO core.pull_request_subscriptions (pull_request_id, user_id, reason)
			SELECT id, $2, $3 FROM core.pull_requests WHERE id = $1
			ON CONFLICT (pull_request_id, user_id) DO NOTHING`, o.pullID, u, reason)
		return err
	}

	switch o.name {
	case domainevents.PullRequestConflicts, domainevents.PullRequestChecksFailed:
		// Solo l'autore, sulla propria PR (PR11). Non è un'azione di nessuno.
		add(o.authorID, ReasonStateChange)
		return e.insert(ctx, tx, o, cands)
	case domainevents.PullRequestOpened:
		if err := autoSub(o.authorID, "author"); err != nil {
			return err
		}
	case domainevents.PullRequestReviewRequested:
		if err := autoSub(o.assignee, "reviewer"); err != nil {
			return err
		}
		add(o.assignee, ReasonReviewRequested) // anche dopo un Unsubscribe
	case domainevents.PullRequestReviewSubmitted, domainevents.PullRequestCommentCreated:
		if o.actor != uuid.Nil {
			if err := autoSub(o.actor, "commenter"); err != nil {
				return err
			}
		}
	}

	// Menzioni (I8): utenti, agenti e team espansi nei membri.
	mentioned, err := e.mentioned(ctx, newMentions(o.text, o.prevText))
	if err != nil {
		return err
	}
	for _, u := range mentioned {
		if ok, err := e.canSee(ctx, &o, u); err != nil {
			return err
		} else if !ok {
			continue
		}
		if err := autoSub(u, "mentioned"); err != nil {
			return err
		}
		add(u, ReasonMentioned) // anche dopo un Unsubscribe
	}

	// Chi segue la PR e chi ha Watch `all` sul repo.
	watchAll := false
	switch o.name {
	case domainevents.PullRequestOpened:
		watchAll = !o.draft // una bozza non avvisa il repo finché non è pronta
	case domainevents.PullRequestReadyForReview, domainevents.PullRequestReviewSubmitted, domainevents.PullRequestCommentCreated:
		watchAll = true
	}
	switch o.name {
	case domainevents.PullRequestReviewSubmitted, domainevents.PullRequestCommentCreated:
		if err := e.addPullFollowers(ctx, tx, o, add, false); err != nil {
			return err
		}
	case domainevents.PullRequestClosed, domainevents.PullRequestReopened, domainevents.PullRequestMerged,
		domainevents.PullRequestReadyForReview:
		if err := e.addPullFollowers(ctx, tx, o, add, true); err != nil {
			return err
		}
	}
	if watchAll {
		rows, err := tx.Query(ctx, `SELECT user_id FROM core.repo_watches WHERE repo_id = $1 AND mode = 'all'`, o.repoID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u uuid.UUID
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			add(u, ReasonSubscribed)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return e.insert(ctx, tx, o, cands)
}

// addPullFollowers aggiunge chi segue la PR: con stateChange il motivo è
// state_change, altrimenti participating (autore, revisori, chi ha commentato,
// i menzionati) e subscribed per Subscribe.
func (e *Engine) addPullFollowers(ctx context.Context, tx pgx.Tx, o occurrence, add func(uuid.UUID, string), stateChange bool) error {
	rows, err := tx.Query(ctx, `SELECT user_id, reason FROM core.pull_request_subscriptions WHERE pull_request_id = $1 AND subscribed`, o.pullID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u uuid.UUID
		var reason string
		if err := rows.Scan(&u, &reason); err != nil {
			return err
		}
		switch {
		case stateChange:
			add(u, ReasonStateChange)
		case reason == "manual":
			add(u, ReasonSubscribed)
		default:
			add(u, ReasonParticipating)
		}
	}
	return rows.Err()
}
