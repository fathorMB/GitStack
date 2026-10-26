package webhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/jackc/pgx/v5"
)

// Webhook deployment (v3-a, GIT-384, H12): contratto WebhookDeploymentPayload in api/openapi.yaml.
// action è started, succeeded, failed o rolled_back; app e deployment sono le istantanee del
// contratto composte da core nella transazione del deploy (store.enqueueAppDeployment). Stessi
// corpo comune, intestazioni, firma HMAC e tentativi degli altri webhook (C6/C7).

var appDeploymentActions = map[string]string{
	domainevents.AppDeploymentStarted: "started", domainevents.AppDeploymentSucceeded: "succeeded",
	domainevents.AppDeploymentFailed: "failed", domainevents.AppDeploymentRolledBack: "rolled_back",
}

// handleAppEvent gestisce le azioni di un deploy. handled=false: l'evento non è di questo gruppo
// (app_status.unhealthy è solo una notifica, non ha un webhook nel contratto).
func (e *Engine) handleAppEvent(ctx context.Context, tx pgx.Tx, ev outboxEvent) (bool, error) {
	a, ok := appDeploymentActions[ev.name]
	if !ok {
		return false, nil
	}
	var p domainevents.AppDeploymentPayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return true, &badEventError{err}
	}
	return true, e.dispatch(ctx, tx, ev, EventDeployment, a, p.Repo, p.Actor, false, func(b *builder) error {
		if len(p.App) == 0 || len(p.Deployment) == 0 {
			return &badEventError{errors.New("evento di deploy senza istantanea")}
		}
		b.body["app"] = json.RawMessage(p.App)
		b.body["deployment"] = json.RawMessage(p.Deployment)
		return nil
	})
}
