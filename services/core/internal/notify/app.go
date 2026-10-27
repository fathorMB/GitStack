package notify

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppUnhealthyNoticeWindow limita a un avviso ogni cinque minuti per app e destinatario.
const AppUnhealthyNoticeWindow = 5 * time.Minute

// Avvisi delle app (v3-a, GIT-384, H12). Chi ha fatto il deploy riceve un avviso quando il deploy
// fallisce (la versione precedente resta in esecuzione) e quando un'app che era in esecuzione ha le
// copie non più pronte («fermata»). Il motivo è state_change, come per la CI; come le altre notifiche,
// niente a chi non legge il repo (insert). Il sistema non è mai il destinatario: senza attore
// (deploy automatico senza chi l'ha avviato) nessuno viene avvisato.
// Non ancora: gli admin del repo (il contratto li prevede per il deploy fallito). Rimandato a GIT-404:
// core non sa elencare gli admin di un repo, serve un'operazione interna di identity.

// isAppNotice dice se l'evento è uno di quelli che avvisano chi ha fatto il deploy.
func isAppNotice(name string) bool {
	return name == domainevents.AppDeploymentFailed || name == domainevents.AppStatusUnhealthy
}

// appNoticePayload sono i campi dell'evento che servono all'avviso.
type appNoticePayload struct {
	Repo    domainevents.Repo  `json:"repo"`
	Actor   *domainevents.User `json:"actor"`
	AppName string             `json:"appName"`
	Version int                `json:"version"`
	Reason  string             `json:"reason"`
}

// handleApp scrive l'avviso di un deploy fallito o di un'app fermata.
func (e *Engine) handleApp(ctx context.Context, tx pgx.Tx, ev outboxEvent) error {
	var p appNoticePayload
	if err := json.Unmarshal(ev.payload, &p); err != nil {
		return &badEventError{err}
	}
	if p.Actor == nil || p.Actor.ID == "" {
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
	data := map[string]any{"app": p.AppName}
	if ev.name == domainevents.AppDeploymentFailed {
		data["version"] = p.Version
		data["reason"] = p.Reason
	}
	o := occurrence{name: ev.name, repoID: repoID, access: map[uuid.UUID]bool{}, data: data}
	if ev.name == domainevents.AppStatusUnhealthy {
		// Conserva il primo avviso immediato, poi sopprime le oscillazioni ravvicinate
		// della stessa app. Il controllo è per destinatario e vede anche gli insert
		// precedenti nella stessa transazione.
		var recent bool
		window := e.AppUnhealthyNoticeWindow
		if window <= 0 {
			window = AppUnhealthyNoticeWindow
		}
		now := time.Now()
		if e.Now != nil {
			now = e.Now()
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM core.notifications
			WHERE user_id = $1 AND repo_id = $2 AND event_name = $3
			  AND data->>'app' = $4
			  AND created_at >= $5
		)`, actor, repoID, ev.name, p.AppName, now.Add(-window)).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return nil
		}
	}
	return e.insert(ctx, tx, o, map[uuid.UUID]string{actor: ReasonStateChange})
}
