package ci

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultStoragePurgeInterval: ogni quanto il giro cerca log, artefatti e cache da eliminare.
const DefaultStoragePurgeInterval = time.Hour

// PurgeStorageOnce fa un giro di pulizia con le impostazioni correnti dell'amministratore (CI5):
// log dei job finiti oltre la conservazione, artefatti scaduti, cache inutilizzate o oltre quota.
// Elimina anche i file rimasti dopo le cascate (tombstone) e i file d'appoggio delle cache
// abbandonati (spoolDir, vuoto = cartella temporanea). logs o blobs nil si saltano. Ritorna quanti log, artefatti e cache ha eliminato.
func PurgeStorageOnce(ctx context.Context, pool *pgxpool.Pool, logs *LogStore, blobs Blobs, spoolDir string, now time.Time) (nLogs, nArtifacts, nCaches int, err error) {
	st, err := LoadSettings(ctx, pool)
	if err != nil {
		return 0, 0, 0, err
	}
	if logs != nil {
		if nLogs, err = logs.PurgeOldLogs(ctx, pool, now, st.LogRetention()); err != nil {
			return
		}
	}
	if blobs != nil {
		if nArtifacts, err = PurgeExpiredArtifacts(ctx, pool, blobs, now); err != nil {
			return
		}
		if nCaches, err = EvictCaches(ctx, pool, blobs, now, nil, st); err != nil {
			return
		}
		if _, err = PurgeTombstones(ctx, pool, blobs); err != nil {
			return
		}
		if _, err = PurgeSpool(ctx, pool, spoolDir, now); err != nil {
			return
		}
	}
	return
}

// RunStoragePurge esegue PurgeStorageOnce ogni `every` finché ctx non finisce. Gli errori si registrano e si ritentano.
func RunStoragePurge(ctx context.Context, pool *pgxpool.Pool, logs *LogStore, blobs Blobs, spoolDir string, every time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		l, a, c, err := PurgeStorageOnce(ctx, pool, logs, blobs, spoolDir, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Warn("pulizia di log, artefatti e cache non riuscita, si ritenta al giro successivo", "err", err)
		}
		if l+a+c > 0 {
			log.Info("pulizia di log, artefatti e cache", "logs", l, "artifacts", a, "caches", c)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
