// Package repopurge è il job periodico di core che cancella definitivamente i
// repo eliminati da più di 7 giorni (R2, M-03/F): dati su disco tramite l'API
// interna di git, grant e attributi in identity, poi la riga in core, che
// libera il nome.
//
// Sicuro con più repliche: ogni repo è preso con FOR UPDATE SKIP LOCKED in
// una transazione propria (vedi store.PurgeDue), quindi due corse in
// parallelo si dividono i repo senza toccarne uno due volte. Idempotente:
// ogni passo tollera di essere già stato fatto (repo già assente su disco,
// grant già tolti), così una corsa interrotta a metà si completa alla
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
	// Now è l'orologio (iniettabile nei test); nil = time.Now.
	Now func() time.Time
	// Log; nil = slog.Default().
	Log *slog.Logger
}

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *Job) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}

// RunOnce cancella i repo scaduti e ritorna quanti ne ha cancellati. Un repo
// che fallisce resta nel cestino e si riprova alla prossima corsa.
func (j *Job) RunOnce(ctx context.Context) (int, error) {
	n, errs := j.Store.PurgeDue(ctx, j.now(), j.cleanup)
	for _, err := range errs {
		j.log().Warn("cancellazione definitiva di un repo non riuscita", "err", err)
	}
	if n > 0 {
		j.log().Info("repo cancellati definitivamente", "count", n)
	}
	return n, errors.Join(errs...)
}

// cleanup toglie dal disco e da identity un repo scaduto, prima che la riga
// sparisca da core.
func (j *Job) cleanup(ctx context.Context, r store.Repo) error {
	// Il servizio git cancella solo dal cestino: se per qualche motivo il
	// repo non c'è finito (eliminazione interrotta) lo si cestina ora. Già
	// nel cestino (409) o già assente (404) non sono errori.
	if err := j.Git.Trash(ctx, systemCaller, r.ID); err != nil &&
		!errors.Is(err, gitclient.ErrConflict) && !errors.Is(err, gitclient.ErrNotFound) {
		return fmt.Errorf("repo %s: cestino su disco: %w", r.ID, err)
	}
	if err := j.Git.Delete(ctx, systemCaller, r.ID); err != nil && !errors.Is(err, gitclient.ErrNotFound) {
		return fmt.Errorf("repo %s: cancellazione su disco: %w", r.ID, err)
	}
	if err := j.Identity.PurgeResource(ctx, r.ID); err != nil {
		return fmt.Errorf("repo %s: grant in identity: %w", r.ID, err)
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
