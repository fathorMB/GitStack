package notify

import (
	"context"
	"encoding/json"
	"fmt"
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
// Il deploy fallito arriva anche agli admin del repo (GIT-404), elencati da identity con
// GET /internal/resources/{id}/members?role=admin (stesse regole di HasRole): chi ha fatto il deploy e
// è anche admin riceve un solo avviso. Senza elenco (Identity non lo sa fare) restano solo gli autori.
// L'app fermata resta solo per chi ha fatto il deploy.

// ResourceMemberLister è l'operazione interna di identity che elenca gli utenti con almeno un ruolo su
// una risorsa. Identity può implementarla o no: il motore la usa se c'è.
type ResourceMemberLister interface {
	ResourceMembers(ctx context.Context, resourceID uuid.UUID, role string) ([]uuid.UUID, error)
}

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
	hasActor := p.Actor != nil && p.Actor.ID != ""
	failed := ev.name == domainevents.AppDeploymentFailed
	lister, _ := e.Identity.(ResourceMemberLister)
	if !hasActor && !(failed && lister != nil) {
		return nil
	}
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		return &badEventError{err}
	}
	var actor uuid.UUID
	if hasActor {
		if actor, err = uuid.Parse(p.Actor.ID); err != nil {
			return &badEventError{err}
		}
	}
	cands := map[uuid.UUID]string{}
	if hasActor {
		cands[actor] = ReasonStateChange
	}
	if failed && lister != nil {
		// Errore di identity: l'evento si ritenta, meglio che perdere l'avviso agli admin.
		admins, err := lister.ResourceMembers(ctx, repoID, "admin")
		if err != nil {
			return fmt.Errorf("admin del repo non elencati: %w", err)
		}
		for _, a := range admins {
			if a != uuid.Nil {
				cands[a] = ReasonStateChange
			}
		}
	}
	data := map[string]any{"app": p.AppName}
	if ev.name == domainevents.AppDeploymentFailed {
		data["version"] = p.Version
		data["reason"] = p.Reason
	}
	o := occurrence{name: ev.name, repoID: repoID, access: map[uuid.UUID]bool{}, data: data}
	if ev.name == domainevents.AppStatusUnhealthy && hasActor {
		// Conserva il primo avviso immediato, poi sopprime le oscillazioni ravvicinate
		// della stessa app. Il controllo è per destinatario e vede anche gli insert
		// precedenti nella stessa transazione.
		var recent bool
		window := e.AppUnhealthyNoticeWindow
		if window <= 0 {
			window = AppUnhealthyNoticeWindow
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM core.notifications
			WHERE user_id = $1 AND repo_id = $2 AND event_name = $3
			  AND data->>'app' = $4
			  AND created_at >= clock_timestamp() - ($5 * interval '1 millisecond')
		)`, actor, repoID, ev.name, p.AppName, window.Milliseconds()).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return nil
		}
	}
	return e.insert(ctx, tx, o, cands)
}
