package ci

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultLogRetention: i log dei job finiti da più di questo tempo si eliminano
// (CI5). Il parametro è della funzione: la manopola dell'amministratore è di GIT-280.
const DefaultLogRetention = 90 * 24 * time.Hour

// DefaultLogPurgeInterval: ogni quanto il giro periodico cerca i log scaduti.
const DefaultLogPurgeInterval = time.Hour

// logPurgeBatch: quanti job per lotto; i lotti pieni proseguono finché ce ne sono.
const logPurgeBatch = 200

// PurgeOldLogs elimina i log dei job completati o annullati prima di now-retention:
// il file <Dir>/<jobId>.log e la riga di core.ci_job_logs. I job in corso, o finiti
// più di recente, non si toccano. Ritorna quanti job sono stati puliti.
// È idempotente: un file che manca non è un errore e una seconda chiamata non trova nulla.
// retention <= 0 vale DefaultLogRetention.
func (l *LogStore) PurgeOldLogs(ctx context.Context, pool *pgxpool.Pool, now time.Time, retention time.Duration) (int, error) {
	if retention <= 0 {
		retention = DefaultLogRetention
	}
	cutoff := now.Add(-retention).UTC()
	total := 0
	for {
		ids, err := purgeCandidates(ctx, pool, cutoff, logPurgeBatch)
		if err != nil {
			return total, err
		}
		purged := 0
		for _, id := range ids {
			ok, err := l.purgeOne(ctx, pool, cutoff, id)
			if err != nil {
				return total, err
			}
			if ok {
				purged++
			}
		}
		total += purged
		// Un lotto che non ha cancellato nulla (tutto già pulito da un'altra replica) chiude il giro.
		if len(ids) < logPurgeBatch || purged == 0 {
			return total, nil
		}
	}
}

// purgeCandidates elenca i job con un log, finiti prima di cutoff.
func purgeCandidates(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT l.job_id FROM core.ci_job_logs l
		JOIN core.ci_jobs j ON j.id = l.job_id
		WHERE j.status IN ('completed', 'cancelled') AND j.completed_at < $1
		ORDER BY j.completed_at, l.job_id LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// purgeOne pulisce un job in una transazione: la riga di core.ci_job_logs è bloccata
// (come in Append), si ricontrolla che il job sia ancora scaduto, si elimina il file
// e poi la riga. Se il file cade dopo il commit, il giro successivo lo trova mancante
// e va avanti. Ritorna false se il job non è più scaduto.
func (l *LogStore) purgeOne(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time, jobID uuid.UUID) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var one int
	err = tx.QueryRow(ctx, `SELECT 1 FROM core.ci_job_logs l
		JOIN core.ci_jobs j ON j.id = l.job_id
		WHERE l.job_id = $1 AND j.status IN ('completed', 'cancelled') AND j.completed_at < $2
		FOR UPDATE OF l`, jobID, cutoff).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := os.Remove(l.path(jobID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.ci_job_logs WHERE job_id = $1`, jobID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// RunPurge esegue PurgeOldLogs ogni `every` finché ctx non finisce. Gli errori si registrano e si ritentano al giro dopo.
func (l *LogStore) RunPurge(ctx context.Context, pool *pgxpool.Pool, every time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		n, err := l.PurgeOldLogs(ctx, pool, time.Now(), DefaultLogRetention)
		if err != nil && ctx.Err() == nil {
			log.Warn("pulizia dei log dei job non riuscita, si ritenta al giro successivo", "err", err)
		}
		if n > 0 {
			log.Info("log dei job eliminati dopo la conservazione", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
