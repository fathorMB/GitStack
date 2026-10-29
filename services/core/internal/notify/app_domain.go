package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Avvisi dei domini e dei certificati (v3-c, GIT-461): dominio che non supera la verifica o non punta
// più all'istanza, rinnovo Let's Encrypt fallito, certificato del cliente in scadenza. Vanno agli admin
// dell'app e dell'istanza: ResourceMembers(repo, admin) dà gli admin del repo e, per le stesse regole di
// HasRole, gli admin di sistema. Senza elenco (identity non lo sa fare) non si avvisa nessuno. Chi
// ripete lo stesso stato lo evita a monte (lo store accoda l'evento solo nel passaggio di stato).

func isAppDomainNotice(name string) bool {
	return name == domainevents.AppDomainDNSFailed || name == domainevents.AppCertificateRenewalFailed ||
		name == domainevents.AppCertificateExpiring
}

func (e *Engine) handleAppDomain(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	var p domainevents.AppDomainPayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return &badEventError{err}
	}
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		return &badEventError{err}
	}
	lister, _ := e.Identity.(ResourceMemberLister)
	if lister == nil {
		return nil
	}
	admins, err := lister.ResourceMembers(ctx, repoID, "admin")
	if err != nil {
		return fmt.Errorf("admin del repo non elencati: %w", err)
	}
	cands := map[uuid.UUID]string{}
	for _, a := range admins {
		if a != uuid.Nil {
			cands[a] = ReasonStateChange
		}
	}
	data := map[string]any{"app": p.AppName, "domain": p.Domain}
	if p.Reason != "" {
		data["reason"] = p.Reason
	}
	if p.ValidUntil != "" {
		data["validUntil"] = p.ValidUntil
	}
	return e.insert(ctx, tx, occurrence{name: ev.name, repoID: repoID, access: map[uuid.UUID]bool{}, data: data}, cands)
}
