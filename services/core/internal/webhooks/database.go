package webhooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Webhook database (v3-d, GIT-568, DB20): la famiglia database.* di docs/events.md, con lo stesso
// motore degli altri webhook (corpo comune v1, intestazioni, firma HMAC, tentativi, C7). Il
// payload non contiene mai segreti: niente password (né dell'owner né dei link), niente Secret,
// niente DATABASE_URL: l'evento di dominio porta solo l'istantanea minima della risorsa.
//
// Destinatari (C6): i webhook d'organizzazione dell'owner del database iscritti a `database`, per
// org_id: l'evento di dominio porta ownerId/ownerType (non segreti), quindi la consegna non
// dipende dai repo dell'org (che possono non esserci) né dalla riga del database (gia' eliminata
// per database.deleted). Un database di un utente non ha webhook: C6 non prevede uno scope
// «utente» e qui non se ne inventa uno.

type databaseKind struct{ action string }

var databaseActions = map[string]databaseKind{
	domainevents.DatabaseCreated:         {"created"},
	domainevents.DatabaseUpdated:         {"updated"},
	domainevents.DatabaseDeleted:         {"deleted"},
	domainevents.DatabaseLinked:          {"linked"},
	domainevents.DatabaseUnlinked:        {"unlinked"},
	domainevents.DatabaseLanEnabled:      {"lan_enabled"},
	domainevents.DatabaseLanDisabled:     {"lan_disabled"},
	domainevents.DatabaseBackupCompleted: {"backup_completed"},
	domainevents.DatabaseBackupFailed:    {"backup_failed"},
	domainevents.DatabaseRestored:        {"restored"},
	// quota_warning non ha webhook: come per le app è solo una notifica (docs/events.md).
}

// handleDatabaseEvent gestisce la famiglia database.*. handled=false: l'evento non è di questo gruppo.
func (e *Engine) handleDatabaseEvent(ctx context.Context, tx pgx.Tx, ev outboxEvent) (bool, error) {
	k, ok := databaseActions[ev.name]
	if !ok {
		return false, nil
	}
	var p domainevents.DatabasePayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return true, &badEventError{err}
	}
	return true, e.dispatchDatabase(ctx, tx, ev, k.action, p)
}

// matchingDatabase trova i webhook attivi d'organizzazione dell'owner iscritti all'evento.
// Solo owner organization: un database di un utente non ha webhook (C6).
func matchingDatabase(ctx context.Context, q pgx.Tx, d domainevents.Database, event string) ([]target, error) {
	if d.OwnerType != "organization" || d.OwnerID == "" {
		return nil, nil
	}
	orgID, err := uuid.Parse(d.OwnerID)
	if err != nil {
		return nil, &badEventError{fmt.Errorf("ownerId dell'evento database non valido: %w", err)}
	}
	rows, err := q.Query(ctx, `SELECT w.id FROM core.webhooks w
		WHERE w.active AND w.scope = 'org' AND $1 = ANY(w.events) AND w.org_id = $2
		ORDER BY w.id`, event, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// dispatchDatabase crea le consegne per l'evento di un database: corpo comune v1 più i campi
// dell'evento di dominio, senza toccarne i valori (il divieto di segreti vale alla pubblicazione).
func (e *Engine) dispatchDatabase(ctx context.Context, tx pgx.Tx, ev outboxEvent, action string, p domainevents.DatabasePayload) error {
	targets, err := matchingDatabase(ctx, tx, p.Database, EventDatabase)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	b := &builder{e: e, body: obj{}}
	b.common(EventDatabase, action, ev.created)
	if err := b.sender(ctx, p.Actor); err != nil {
		return err
	}
	db := obj{"id": p.Database.ID, "owner": p.Database.Owner, "name": p.Database.Name, "pgMajor": p.Database.PgMajor}
	b.body["database"] = db
	if p.SizeBytes != nil {
		b.body["sizeBytes"] = *p.SizeBytes
	}
	if p.CPUMillis != nil {
		b.body["cpuMillis"] = *p.CPUMillis
	}
	if p.MemoryBytes != nil {
		b.body["memoryBytes"] = *p.MemoryBytes
	}
	if p.Changes != nil {
		b.body["changes"] = json.RawMessage(p.Changes)
	}
	if p.DeletedAt != "" {
		b.body["deletedAt"] = p.DeletedAt
	}
	if p.Link != nil {
		b.body["link"] = json.RawMessage(p.Link)
	}
	if p.DiskState != "" {
		b.body["diskState"] = p.DiskState
	}
	if p.UsageBytes != nil {
		b.body["usageBytes"] = *p.UsageBytes
	}
	raw, err := json.Marshal(b.body)
	if err != nil {
		return &badEventError{err}
	}
	return e.enqueue(ctx, tx, targets, EventDatabase, action, ev.id, raw)
}
