package store

import (
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// AppToAPI converte i campi comuni dell'app nel modello esposto dall'API.
func AppToAPI(a App) openapi.App {
	status, reason := a.Effective()
	ready := a.ReadyCopies
	if status == "stopped" {
		ready = 0
	}
	out := openapi.App{
		Id: openapi_types.UUID(a.ID), Name: openapi.AppName(a.Name), Owner: a.Owner, Repo: a.RepoName,
		Status: openapi.AppStatus(status), Access: openapi.AppAccessMode(a.Access), Image: AppImageToAPI(a.Image),
		ActiveVersion: a.ActiveVersion, Copies: a.Config.Copies, ReadyCopies: ready,
		Resources: AppResourcesToAPI(a.Config), CreatedAt: a.CreatedAt, CreatedBy: AppActorToAPI(a.CreatedBy), UpdatedAt: a.UpdatedAt,
	}
	if reason != nil {
		r := openapi.AppStoppedReason(*reason)
		out.StoppedReason = &r
	}
	return out
}

// AppVersionToAPI converte una versione, compreso il suo stato nei backup.
func AppVersionToAPI(v AppVersion, active, previous int) openapi.AppVersion {
	return openapi.AppVersion{
		Number: v.Number, Trigger: openapi.AppVersionTrigger(v.Trigger), State: openapi.AppDeployState(v.State),
		StateReason: v.StateReason, Image: AppImageToAPI(v.Image), ImageAvailable: true,
		InBackup: v.Number == active || v.Number == previous, Branch: v.Branch, CommitSha: v.CommitSha,
		Summary: v.Summary, RollbackOf: v.RollbackOf, CreatedAt: v.CreatedAt, CreatedBy: AppActorToAPI(v.CreatedBy),
	}
}

func AppActorToAPI(a *AppActor) *openapi.AppActor {
	if a == nil {
		return nil
	}
	return &openapi.AppActor{Id: openapi_types.UUID(a.ID), Username: a.Username, Type: openapi.AppActorType(a.Type)}
}

func AppHealthToAPI(h *AppHealthCheck) *openapi.AppHealthCheck {
	if h == nil {
		return nil
	}
	return &openapi.AppHealthCheck{Path: h.Path, IntervalSeconds: h.IntervalSeconds, TimeoutSeconds: h.TimeoutSeconds}
}

func AppResourcesToAPI(c AppConfig) openapi.AppResources {
	return openapi.AppResources{
		Guaranteed: openapi.AppResourceAmount{CpuMillis: c.Guaranteed.CPUMillis, MemoryBytes: c.Guaranteed.MemoryBytes},
		Limit:      openapi.AppResourceAmount{CpuMillis: c.Limit.CPUMillis, MemoryBytes: c.Limit.MemoryBytes},
	}
}

func AppImageToAPI(i AppImage) openapi.AppImage {
	return openapi.AppImage{Reference: i.Reference, Digest: i.Digest, Source: openapi.AppImageSource(i.Source),
		Verified: i.Verified, SignedBy: i.SignedBy, RunsAsRoot: i.RunsAsRoot}
}
