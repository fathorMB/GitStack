package webhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/jackc/pgx/v5"
)

// Webhook della CI (v2-a, GIT-281, CI6): workflow_run e workflow_job. Contratto:
// schemi WebhookWorkflowRunPayload e WebhookWorkflowJobPayload di
// api/openapi.yaml. Come per le PR, l'esecuzione e il job nel payload sono
// l'istantanea al momento dell'evento (composta da core nella transazione della
// modifica): la consegna non rilegge nulla.

var workflowRunActions = map[string]string{
	domainevents.WorkflowRunQueued: "queued", domainevents.WorkflowRunInProgress: "in_progress",
	domainevents.WorkflowRunCompleted: "completed", domainevents.WorkflowRunCancelled: "cancelled",
}

var workflowJobActions = map[string]string{
	domainevents.WorkflowJobQueued: "queued", domainevents.WorkflowJobInProgress: "in_progress",
	domainevents.WorkflowJobCompleted: "completed", domainevents.WorkflowJobCancelled: "cancelled",
}

// handleCIEvent gestisce gli eventi di esecuzioni e job. handled=false: l'evento
// non è di questo gruppo.
func (e *Engine) handleCIEvent(ctx context.Context, tx pgx.Tx, ev outboxEvent) (bool, error) {
	if a, ok := workflowRunActions[ev.name]; ok {
		var p domainevents.WorkflowRunPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return true, &badEventError{err}
		}
		return true, e.dispatch(ctx, tx, ev, EventWorkflowRun, a, p.Repo, p.Actor, false, func(b *builder) error {
			if len(p.Run) == 0 {
				return &badEventError{errors.New("evento di esecuzione senza istantanea")}
			}
			b.body["workflowRun"] = json.RawMessage(p.Run)
			return nil
		})
	}
	if a, ok := workflowJobActions[ev.name]; ok {
		var p domainevents.WorkflowJobPayload
		if err := json.Unmarshal(ev.payload, &p); err != nil {
			return true, &badEventError{err}
		}
		return true, e.dispatch(ctx, tx, ev, EventWorkflowJob, a, p.Repo, p.Actor, false, func(b *builder) error {
			if len(p.Run) == 0 || len(p.Job) == 0 {
				return &badEventError{errors.New("evento di job senza istantanea")}
			}
			b.body["workflowRun"] = json.RawMessage(p.Run)
			b.body["workflowJob"] = json.RawMessage(p.Job)
			return nil
		})
	}
	return false, nil
}
