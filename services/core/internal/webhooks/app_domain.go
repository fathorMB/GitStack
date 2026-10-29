package webhooks

import (
	"context"
	"encoding/json"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/jackc/pgx/v5"
)

// Webhook app_domain (v3-c, GIT-492): contratto WebhookAppDomainPayload in api/openapi.yaml. Esce per
// repo (quello dell'app), come deployment, dagli stessi eventi di dominio da cui GIT-461 crea la
// notifica: lo store li accoda una volta sola per guasto o scadenza, quindi un episodio = un invio.
// Il payload ha solo nome dell'app, dominio, stato, motivo e scadenza: mai chiavi né Secret.

type appDomainKind struct{ action, status string }

var appDomainActions = map[string]appDomainKind{
	domainevents.AppDomainDNSFailed:          {"dns_failed", "dns_failed"},
	domainevents.AppCertificateRenewalFailed: {"certificate_renewal_failed", "renewal_failed"},
	domainevents.AppCertificateExpiring:      {"certificate_expiring", "expiring"},
}

func (e *Engine) handleAppDomainEvent(ctx context.Context, tx pgx.Tx, ev outboxEvent) (bool, error) {
	k, ok := appDomainActions[ev.name]
	if !ok {
		return false, nil
	}
	var p domainevents.AppDomainPayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return true, &badEventError{err}
	}
	return true, e.dispatch(ctx, tx, ev, EventAppDomain, k.action, p.Repo, nil, false, func(b *builder) error {
		b.body["app"] = obj{"name": p.AppName}
		b.body["domain"] = p.Domain
		b.body["status"] = k.status
		if p.Reason != "" {
			b.body["reason"] = p.Reason
		}
		if p.ValidUntil != "" {
			b.body["validUntil"] = p.ValidUntil
		}
		return nil
	})
}
