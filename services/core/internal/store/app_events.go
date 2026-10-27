package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/jackc/pgx/v5"
)

// Eventi delle app nell'outbox (v3-a, GIT-384, H12). Gli eventi si scrivono nella stessa transazione
// della modifica che li causa: un deploy fallito o un'app fermata ha una riga in outbox se e solo se
// la modifica è avvenuta. Le istantanee App e AppVersion hanno i nomi del contratto
// (WebhookDeploymentPayload): sono composte qui perché la consegna non rilegge il database.

type appWire struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Owner         string             `json:"owner"`
	Repo          string             `json:"repo"`
	Status        string             `json:"status"`
	StoppedReason *string            `json:"stoppedReason,omitempty"`
	Access        string             `json:"access"`
	Image         openapi.AppImage   `json:"image"`
	ActiveVersion int                `json:"activeVersion"`
	Copies        int                `json:"copies"`
	ReadyCopies   int                `json:"readyCopies"`
	Resources     appResourcesWire   `json:"resources"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
	CreatedBy     *domainevents.User `json:"createdBy,omitempty"`
}

type versionWire struct {
	Number         int                `json:"number"`
	Trigger        string             `json:"trigger"`
	State          string             `json:"state"`
	StateReason    *string            `json:"stateReason,omitempty"`
	Image          openapi.AppImage   `json:"image"`
	ImageAvailable bool               `json:"imageAvailable"`
	InBackup       bool               `json:"inBackup"`
	Branch         *string            `json:"branch,omitempty"`
	CommitSha      *string            `json:"commitSha,omitempty"`
	Summary        *string            `json:"summary,omitempty"`
	RollbackOf     *int               `json:"rollbackOf,omitempty"`
	CreatedAt      time.Time          `json:"createdAt"`
	CreatedBy      *domainevents.User `json:"createdBy,omitempty"`
}

// appSnapshot è l'App del contratto al momento dell'evento (stato e copie pronte come li mostra l'API).
func appSnapshot(a App) appWire {
	converted := AppToAPI(a)
	var reason *string
	if converted.StoppedReason != nil {
		value := string(*converted.StoppedReason)
		reason = &value
	}
	return appWire{
		ID: converted.Id.String(), Name: string(converted.Name), Owner: converted.Owner, Repo: converted.Repo,
		Status: string(converted.Status), StoppedReason: reason, Access: string(converted.Access), Image: converted.Image,
		ActiveVersion: converted.ActiveVersion, Copies: converted.Copies, ReadyCopies: converted.ReadyCopies,
		Resources: appResourcesWire{Guaranteed: AppResourceAmount{CPUMillis: converted.Resources.Guaranteed.CpuMillis, MemoryBytes: converted.Resources.Guaranteed.MemoryBytes},
			Limit: AppResourceAmount{CPUMillis: converted.Resources.Limit.CpuMillis, MemoryBytes: converted.Resources.Limit.MemoryBytes}},
		CreatedAt: converted.CreatedAt, UpdatedAt: converted.UpdatedAt, CreatedBy: actorUser(a.CreatedBy),
	}
}

type appResourcesWire struct {
	Guaranteed AppResourceAmount `json:"guaranteed"`
	Limit      AppResourceAmount `json:"limit"`
}

// versionSnapshot è la AppVersion del contratto. InBackup: attiva e precedente (H4).
func versionSnapshot(v AppVersion, active int) versionWire {
	converted := AppVersionToAPI(v, active, previousAppVersion(active))
	return versionWire{
		Number: converted.Number, Trigger: string(converted.Trigger), State: string(converted.State), StateReason: converted.StateReason,
		Image: converted.Image, ImageAvailable: converted.ImageAvailable, InBackup: converted.InBackup,
		Branch: converted.Branch, CommitSha: converted.CommitSha, Summary: converted.Summary, RollbackOf: converted.RollbackOf,
		CreatedAt: converted.CreatedAt, CreatedBy: actorUser(v.CreatedBy),
	}
}

func previousAppVersion(active int) int {
	if active > 1 {
		return active - 1
	}
	return 0
}

func actorUser(a *AppActor) *domainevents.User {
	if a == nil {
		return nil
	}
	return &domainevents.User{ID: a.ID.String(), Username: a.Username, Type: a.Type}
}

func appRepo(a App) domainevents.Repo {
	return domainevents.Repo{ID: a.RepoID.String(), FullName: a.Owner + "/" + a.RepoName}
}

// enqueueAppDeployment scrive l'azione di un deploy (app_deployment.*) nella transazione tx.
func enqueueAppDeployment(ctx context.Context, tx pgx.Tx, a App, v AppVersion, action, reason string) error {
	app, err := json.Marshal(appSnapshot(a))
	if err != nil {
		return err
	}
	dep, err := json.Marshal(versionSnapshot(v, a.ActiveVersion))
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, tx, action, domainevents.Version, domainevents.AppDeploymentPayload{
		Repo: appRepo(a), Actor: actorUser(v.CreatedBy), AppName: a.Name, Version: v.Number, Reason: reason,
		App: app, Deployment: dep,
	})
}

// enqueueAppUnhealthy scrive app_status.unhealthy: l'app era in esecuzione e ora ha copie non pronte.
// L'avviso va a chi ha fatto il deploy della versione attiva (actor).
func enqueueAppUnhealthy(ctx context.Context, tx pgx.Tx, a App, actor *AppActor) error {
	app, err := json.Marshal(appSnapshot(a))
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, tx, domainevents.AppStatusUnhealthy, domainevents.Version, domainevents.AppStatusPayload{
		Repo: appRepo(a), Actor: actorUser(actor), AppName: a.Name, App: app,
	})
}
