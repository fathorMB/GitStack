package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// EventAppDeployFailed è l'evento della notifica di un deploy fallito (H13).
const EventAppDeployFailed = "app.deploy_failed"

// deployNotify sono le dipendenze dell'avviso «deploy fallito», separate dal database per poterle provare.
type deployNotify struct {
	// owner dà proprietario del repo (user|org e id).
	owner func(ctx context.Context, repoID uuid.UUID) (ownerType string, ownerID uuid.UUID, err error)
	// mgr dice chi ha ancora admin sul repo; nil: nessun avviso (mai a chi non può gestire l'app).
	mgr webhooks.Managers
	// send scrive le notifiche.
	send func(ctx context.Context, recipients []uuid.UUID, o notify.OccurrenceInput) error
	log  *slog.Logger
}

// admins sono i destinatari: il proprietario del repo (o gli owner dell'organizzazione) che hanno ancora
// admin sul repo. Un agente con solo write non riceve nulla, come pure chi ha fatto il deploy senza admin.
func (d *deployNotify) admins(ctx context.Context, repoID uuid.UUID) []uuid.UUID {
	if d.mgr == nil {
		return nil
	}
	ownerType, ownerID, err := d.owner(ctx, repoID)
	if err != nil {
		d.log.Warn("apps: proprietario del repo non letto, nessun avviso del deploy fallito", "repo", repoID, "err", err)
		return nil
	}
	var cands []uuid.UUID
	if ownerType == "user" {
		cands = append(cands, ownerID)
	} else if owners, err := d.mgr.OrgOwners(ctx, ownerID); err != nil {
		d.log.Warn("apps: owner dell'organizzazione non risolti", "repo", repoID, "err", err)
	} else {
		cands = append(cands, owners...)
	}
	var out []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, u := range cands {
		if u == uuid.Nil || seen[u] {
			continue
		}
		seen[u] = true
		ok, err := d.mgr.HasRole(ctx, u, repoID, "admin")
		if err != nil {
			d.log.Warn("apps: verifica del permesso admin non riuscita", "repo", repoID, "err", err)
			continue
		}
		if ok {
			out = append(out, u)
		}
	}
	return out
}

func (d *deployNotify) failed(ctx context.Context, app store.App, v store.AppVersion, reason string) {
	to := d.admins(ctx, app.RepoID)
	if len(to) == 0 {
		return
	}
	data := map[string]any{"app": app.Name, "owner": app.Owner, "repo": app.RepoName, "version": v.Number, "trigger": v.Trigger, "reason": reason}
	if v.CreatedBy != nil {
		data["triggeredBy"] = v.CreatedBy.Username
		data["triggeredByType"] = v.CreatedBy.Type
	}
	// Il deploy lo ha fatto il sistema o un agente: l'attore della notifica è assente, così nessun admin la perde.
	if err := d.send(ctx, to, notify.OccurrenceInput{EventName: EventAppDeployFailed, RepoID: app.RepoID, Data: data}); err != nil {
		d.log.Warn("apps: notifica del deploy fallito non scritta", "app", app.ID, "version", v.Number, "err", err)
	}
}

// newDeployFailedNotifier collega il controller delle app al motore delle notifiche. Senza motore (identity
// non configurata) o senza la lista di chi ha admin non si avvisa nessuno: mai una notifica a chi non
// potrebbe vedere il repo.
func newDeployFailedNotifier(pool *pgxpool.Pool, eng *notify.Engine, lookup any, logger *slog.Logger) func(context.Context, store.App, store.AppVersion, string) {
	mgr, _ := lookup.(webhooks.Managers)
	if eng == nil || mgr == nil {
		return nil
	}
	d := &deployNotify{
		mgr: mgr,
		log: logger,
		owner: func(ctx context.Context, repoID uuid.UUID) (string, uuid.UUID, error) {
			var t string
			var id uuid.UUID
			err := pool.QueryRow(ctx, `SELECT owner_type, owner_id FROM core.repositories WHERE resource_id = $1`, repoID).Scan(&t, &id)
			return t, id, err
		},
		send: func(ctx context.Context, to []uuid.UUID, o notify.OccurrenceInput) error {
			tx, err := pool.Begin(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if err := eng.Notify(ctx, tx, notify.ReasonStateChange, to, o); err != nil {
				return err
			}
			return tx.Commit(ctx)
		},
	}
	return d.failed
}
