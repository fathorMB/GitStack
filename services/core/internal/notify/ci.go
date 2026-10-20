package notify

import (
	"context"
	"encoding/json"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Notifiche della CI (v2-a, GIT-281, CI6). Chi ha avviato un'esecuzione riceve
// un avviso quando il workflow fallisce e quando torna verde (la precedente
// esecuzione completata dello stesso workflow sullo stesso ref era fallita). Il
// motivo è state_change. Un'esecuzione senza attore (uno schedule) non avvisa
// nessuno; come per le altre notifiche, niente a chi non legge il repo e niente
// a chi ha Watch ignore (insert). Il sistema non è mai il destinatario.

// handleRun gestisce workflow_run.completed.
func (e *Engine) handleRun(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	var p domainevents.WorkflowRunPayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return &badEventError{err}
	}
	if p.Actor == nil || p.Actor.ID == "" {
		return nil
	}
	var run struct {
		ID         string  `json:"id"`
		Conclusion *string `json:"conclusion"`
		Ref        string  `json:"ref"`
		Sha        string  `json:"sha"`
		Event      string  `json:"event"`
	}
	if err := json.Unmarshal(p.Run, &run); err != nil {
		return &badEventError{err}
	}
	concl := ""
	if run.Conclusion != nil {
		concl = *run.Conclusion
	}
	recovered := concl == "success" && p.Previous == "failure"
	if concl != "failure" && !recovered {
		return nil
	}
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		return &badEventError{err}
	}
	actor, err := uuid.Parse(p.Actor.ID)
	if err != nil {
		return &badEventError{err}
	}
	o := occurrence{name: ev.name, repoID: repoID, access: map[uuid.UUID]bool{},
		data: map[string]any{"runId": run.ID, "conclusion": concl, "recovered": recovered,
			"ref": run.Ref, "sha": run.Sha, "event": run.Event}}
	return e.insert(ctx, tx, o, map[uuid.UUID]string{actor: ReasonStateChange})
}
