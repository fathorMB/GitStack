package webhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Webhook delle Pull Request (v1.1, GIT-265, PR11): pull_request,
// pull_request_review e commit_status. Contratto: schemi
// WebhookPullRequestPayload, WebhookPullRequestReviewPayload e
// WebhookCommitStatusPayload di api/openapi.yaml, docs/webhooks.md.
//
// A differenza delle issues, la PR (e la review, e lo stato) del payload è lo
// stato al momento dell'evento: la compone core nella transazione della
// modifica e la mette nell'outbox (domainevents.PullPayload.Snapshot), così il
// webhook non rilegge git e la consegna non dipende dal servizio git.

var pullActions = map[string]string{
	domainevents.PullRequestOpened: "opened", domainevents.PullRequestEdited: "edited",
	domainevents.PullRequestClosed: "closed", domainevents.PullRequestReopened: "reopened",
	domainevents.PullRequestMerged: "merged", domainevents.PullRequestReadyForReview: "ready_for_review",
	domainevents.PullRequestConvertedToDraft: "converted_to_draft", domainevents.PullRequestSynchronize: "synchronize",
	domainevents.PullRequestReviewRequested:      "review_requested",
	domainevents.PullRequestReviewRequestRemoved: "review_request_removed",
}

var pullReviewActions = map[string]string{
	domainevents.PullRequestReviewSubmitted: "submitted",
	domainevents.PullRequestReviewDismissed: "dismissed",
}

// pullSummaryKeys sono i campi di PullRequestSummary: la «sintesi» di
// pull_request_review è la PR senza il resto.
var pullSummaryKeys = []string{"number", "title", "state", "draft", "author", "sourceBranch", "targetBranch",
	"reviewDecision", "statusState", "requestedReviewers", "commentCount", "mergedAt", "closedAt", "createdAt", "updatedAt"}

// handlePullEvent gestisce gli eventi delle PR e degli stati dei commit.
// handled=false: l'evento non è di questo gruppo.
func (e *Engine) handlePullEvent(ctx context.Context, tx pgx.Tx, ev outboxEvent) (bool, error) {
	if a, ok := pullActions[ev.name]; ok {
		var p domainevents.PullPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return true, &badEventError{err}
		}
		return true, e.dispatch(ctx, tx, ev, EventPullRequest, a, p.Repo, p.Actor, false, func(b *builder) error {
			if len(p.Snapshot) == 0 {
				return &badEventError{errors.New("evento di PR senza istantanea")}
			}
			b.body["pullRequest"] = json.RawMessage(p.Snapshot)
			if p.RequestedReviewer != nil {
				u, err := b.issueUser(ctx, p.RequestedReviewer)
				if err != nil {
					return err
				}
				b.body["requestedReviewer"] = u
			}
			return nil
		})
	}
	if a, ok := pullReviewActions[ev.name]; ok {
		var p domainevents.PullPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return true, &badEventError{err}
		}
		return true, e.dispatch(ctx, tx, ev, EventPullRequestReview, a, p.Repo, p.Actor, false, func(b *builder) error {
			if len(p.Snapshot) == 0 || len(p.ReviewSnapshot) == 0 {
				return &badEventError{errors.New("evento di review senza istantanea")}
			}
			sum, err := pullSummary(p.Snapshot)
			if err != nil {
				return &badEventError{err}
			}
			b.body["pullRequest"] = sum
			b.body["review"] = json.RawMessage(p.ReviewSnapshot)
			return nil
		})
	}
	if ev.name == domainevents.CommitStatusCreated {
		var p domainevents.CommitStatusPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return true, &badEventError{err}
		}
		return true, e.dispatch(ctx, tx, ev, EventCommitStatus, "created", p.Repo, p.Actor, false, func(b *builder) error {
			if len(p.Status) == 0 {
				return &badEventError{errors.New("commit_status senza stato")}
			}
			branches := p.Branches
			if branches == nil {
				branches = []string{}
			}
			if len(branches) > 20 {
				branches = branches[:20]
			}
			b.body["commitStatus"] = json.RawMessage(p.Status)
			b.body["combinedState"] = p.Combined
			b.body["branches"] = branches
			return nil
		})
	}
	return false, nil
}

// pullSummary riduce la PR del contratto a PullRequestSummary.
func pullSummary(snapshot json.RawMessage) (obj, error) {
	var full map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &full); err != nil {
		return nil, err
	}
	out := obj{}
	for _, k := range pullSummaryKeys {
		if v, ok := full[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// issueUser è l'utente come IssueUser del contratto (id, username, kind).
func (b *builder) issueUser(ctx context.Context, u *domainevents.User) (obj, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return nil, &badEventError{err}
	}
	if err := b.resolve(ctx, u.ID); err != nil {
		return nil, err
	}
	r := b.users[id]
	name, kind := r.Username, r.Type
	if name == "" {
		name = u.Username
		if u.Type != "" {
			kind = u.Type
		}
	}
	if kind == "" {
		kind = "human"
	}
	return obj{"id": id.String(), "username": name, "kind": kind}, nil
}
