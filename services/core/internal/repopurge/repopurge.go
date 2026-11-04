// Package repopurge toglie ciò che di un repo eliminato sta fuori da Postgres
// (R2, GIT-530): dati su disco e immagini del registry tramite l'API interna
// di git, app e volumi nel cluster, file degli allegati, grant e attributi in
// identity.
//
// L'eliminazione è definitiva e immediata: la riga del repo sparisce da core
// nella transazione della DELETE (con il nome, issue, PR, release, webhook,
// mirror e credenziali), e il repo entra nella coda core.repo_cleanup.
// Purge la svuota subito, a risposta ancora da dare; se un passo non riesce
// (git giù, PVC lento) la riga resta in coda e RunOnce, ogni minuto, riprova.
// Il nome non dipende da nessuno di questi passi.
//
// Sicuro con più repliche: ogni repo è preso con FOR UPDATE SKIP LOCKED in una
// transazione propria (store.CleanupRepo). Idempotente: ogni passo tollera di
// essere già stato fatto, così una corsa interrotta si completa alla
// successiva.
package repopurge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Identità con cui il job parla a git: il servizio verifica solo la firma.
var systemCaller = trust.Identity{UserID: uuid.Nil.String(), Username: "system:repo-purge"}

// Access è la parte di identity che serve al job.
type Access interface {
	// PurgeResource toglie grant e attributi della risorsa (idempotente).
	PurgeResource(ctx context.Context, resourceID uuid.UUID) error
}

// Job è il job di pulizia.
type Job struct {
	Store    *store.Store
	Git      gitclient.Git
	Identity Access
	// Attachments, se presente, toglie i file degli allegati del repo (I9).
	Attachments interface{ RemoveRepo(repoID uuid.UUID) error }
	// ReleaseAssets, se presente, toglie i file degli allegati delle release (REL1): stanno in un volume
	// a parte, quindi RemoveRepo di Attachments non li raggiunge.
	ReleaseAssets interface{ RemoveRepo(repoID uuid.UUID) error }
	// Apps, se presente, toglie dal cluster app e volumi del repo (H5): i PVC vanno via
	// con il repo, e la riga in core li libera dalla quota dell'owner.
	Apps interface {
		ForgetApps(ctx context.Context, appIDs []uuid.UUID) error
	}
	// Log; nil = slog.Default().
	Log *slog.Logger
}

func (j *Job) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}

// Purge toglie ora, in questo processo, tutto ciò che resta del repo `id`
// (già cancellato da core e in coda). Un errore non annulla l'eliminazione:
// il repo resta in coda per RunOnce. Se un'altra replica lo sta già
// trattando ritorna nil.
func (j *Job) Purge(ctx context.Context, id uuid.UUID) error {
	_, err := j.Store.CleanupRepo(ctx, id, nil, j.cleanup)
	return err
}

// RunOnce svuota la coda e ritorna quanti repo ha tolto. Un repo che fallisce
// resta in coda e si riprova alla prossima corsa; gli altri proseguono.
// All'avvio di core è anche il passo di upgrade che cancella i repo che erano
// nel cestino (migrazione 0056): una riga di log per ciascuno.
func (j *Job) RunOnce(ctx context.Context) (int, error) {
	var errs []error
	var skip []uuid.UUID
	n := 0
	for ctx.Err() == nil {
		id, err := j.Store.CleanupRepo(ctx, uuid.Nil, skip, j.cleanup)
		if id == uuid.Nil {
			if err != nil {
				errs = append(errs, err)
			}
			break
		}
		if err != nil {
			j.log().Warn("pulizia di un repo eliminato non riuscita: si riprova", "repoId", id, "err", err)
			errs = append(errs, err)
			skip = append(skip, id)
			continue
		}
		n++
	}
	if n > 0 {
		j.log().Info("repo eliminati: pulizia completata", "count", n)
	}
	return n, errors.Join(errs...)
}

// cleanup toglie dal disco, dal cluster e da identity un repo già cancellato
// da core.
func (j *Job) cleanup(ctx context.Context, c store.RepoCleanup) error {
	id := c.RepoID
	// Il servizio git cancella solo dal cestino: se per qualche motivo il
	// repo non c'è finito lo si cestina ora. Già nel cestino (409) o già
	// assente (404) non sono errori.
	if err := j.Git.Trash(ctx, systemCaller, id); err != nil &&
		!errors.Is(err, gitclient.ErrConflict) && !errors.Is(err, gitclient.ErrNotFound) {
		return fmt.Errorf("repo %s: spostamento su disco: %w", id, err)
	}
	if err := j.Git.Delete(ctx, systemCaller, id); err != nil && !errors.Is(err, gitclient.ErrNotFound) {
		return fmt.Errorf("repo %s: cancellazione su disco: %w", id, err)
	}
	if j.Apps != nil {
		if err := j.Apps.ForgetApps(ctx, c.AppIDs); err != nil {
			return fmt.Errorf("repo %s: app e volumi: %w", id, err)
		}
	}
	if j.Attachments != nil {
		if err := j.Attachments.RemoveRepo(id); err != nil {
			return fmt.Errorf("repo %s: allegati su disco: %w", id, err)
		}
	}
	if j.ReleaseAssets != nil {
		if err := j.ReleaseAssets.RemoveRepo(id); err != nil {
			return fmt.Errorf("repo %s: allegati delle release su disco: %w", id, err)
		}
	}
	if err := j.Identity.PurgeResource(ctx, id); err != nil {
		return fmt.Errorf("repo %s: grant in identity: %w", id, err)
	}
	if c.Source == "trash" {
		j.log().Info("upgrade: repo che era nel cestino eliminato definitivamente", "repo", c.OwnerName+"/"+c.Name, "repoId", id)
	} else {
		j.log().Info("repo eliminato definitivamente", "repo", c.OwnerName+"/"+c.Name, "repoId", id)
	}
	return nil
}

// Run esegue RunOnce ogni `every` finché il contesto non finisce, con una
// prima corsa subito.
func (j *Job) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_, _ = j.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
