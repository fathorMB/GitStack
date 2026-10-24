package ci

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueueStats sono i numeri di GET /admin/ci/queue: coda, job in corso, attesa media e spazio occupato.
type QueueStats struct {
	Queued, Running int
	// AvgWaitSeconds: attesa media in coda (da queued_at a started_at) dei job partiti nelle ultime 24 ore; 0 se nessuno.
	AvgWaitSeconds int
	LogBytes       int64
	ArtifactBytes  int64
	CacheBytes     int64
}

// QueueWindow è la finestra su cui si calcola l'attesa media.
const QueueWindow = 24 * time.Hour

// ReadQueueStats legge coda, attesa media e spazio di log, artefatti (non scaduti) e cache (confermate).
func ReadQueueStats(ctx context.Context, pool *pgxpool.Pool, now time.Time) (QueueStats, error) {
	var s QueueStats
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.ci_jobs WHERE status = 'queued'),
		(SELECT count(*) FROM core.ci_jobs WHERE status = 'in_progress'),
		COALESCE((SELECT round(avg(extract(epoch FROM started_at - queued_at)))::bigint FROM core.ci_jobs
			WHERE started_at IS NOT NULL AND queued_at IS NOT NULL AND started_at >= $1), 0),
		COALESCE((SELECT sum(bytes) FROM core.ci_job_logs), 0)::bigint,
		COALESCE((SELECT sum(size_bytes) FROM core.ci_artifacts WHERE expires_at > $2), 0)::bigint,
		COALESCE((SELECT sum(size_bytes) FROM core.ci_caches WHERE ready_at IS NOT NULL), 0)::bigint`,
		now.Add(-QueueWindow), now).Scan(&s.Queued, &s.Running, &s.AvgWaitSeconds, &s.LogBytes, &s.ArtifactBytes, &s.CacheBytes)
	if s.AvgWaitSeconds < 0 {
		s.AvgWaitSeconds = 0
	}
	return s, err
}
