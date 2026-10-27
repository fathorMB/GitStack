package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Metriche delle app (v3-a, GIT-384, H12). Il controller scrive un campione per copia a ogni
// raccolta: pronto, riavvii del container e, se la sorgente (metrics-server) ha risposto, CPU e
// memoria. Un campione senza CPU né memoria vuol dire «sorgente non risposta», mai zero. Lo store
// non interpreta le serie: le aggrega in lettura.

// AppMetricSample è un campione di una copia.
type AppMetricSample struct {
	Copy        string
	At          time.Time
	Ready       bool
	Restarts    int
	CPUMillis   *int
	MemoryBytes *int64
}

// AppMetricBucket è la media di una copia in un passo di tempo (CPU e memoria nil se assenti).
type AppMetricBucket struct {
	Time        time.Time
	Copy        string
	CPUMillis   *float64
	MemoryBytes *float64
}

// AppCopyState è l'ultimo campione di una copia.
type AppCopyState struct {
	Copy     string
	Ready    bool
	Restarts int
}

// AppendAppMetricSamples scrive i campioni di una raccolta in un'unica transazione.
func (s *Store) AppendAppMetricSamples(ctx context.Context, appID uuid.UUID, samples []AppMetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, m := range samples {
		if _, err := tx.Exec(ctx, `INSERT INTO core.app_metric_samples (app_id, copy, at, ready, restarts, cpu_millicores, memory_bytes)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, appID, m.Copy, m.At.UTC().Truncate(time.Microsecond), m.Ready, m.Restarts, m.CPUMillis, m.MemoryBytes); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AppMetricBuckets aggrega i campioni da since a ora in passi di step secondi: per copia, la media
// del passo. copy vuoto non filtra. Le righe sono ordinate per tempo e copia.
func (s *Store) AppMetricBuckets(ctx context.Context, appID uuid.UUID, since time.Time, stepSeconds int, copy string) ([]AppMetricBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT FLOOR(EXTRACT(EPOCH FROM at) / $3)::bigint AS bucket, copy,
		       AVG(cpu_millicores)::float8, AVG(memory_bytes)::float8
		FROM core.app_metric_samples
		WHERE app_id = $1 AND at >= $2 AND ($4 = '' OR copy = $4)
		GROUP BY bucket, copy
		ORDER BY bucket, copy`, appID, since.UTC(), stepSeconds, copy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppMetricBucket{}
	for rows.Next() {
		var b int64
		var m AppMetricBucket
		if err := rows.Scan(&b, &m.Copy, &m.CPUMillis, &m.MemoryBytes); err != nil {
			return nil, err
		}
		m.Time = time.Unix(b*int64(stepSeconds), 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppMetricsSourceOK dice se, da since in poi, almeno un campione ha la CPU: la sorgente ha risposto.
func (s *Store) AppMetricsSourceOK(ctx context.Context, appID uuid.UUID, since time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.app_metric_samples
		WHERE app_id = $1 AND at >= $2 AND cpu_millicores IS NOT NULL)`, appID, since.UTC()).Scan(&ok)
	return ok, err
}

// AppCopyStates dà l'ultimo campione di ogni copia da since in poi (pronto e riavvii).
func (s *Store) AppCopyStates(ctx context.Context, appID uuid.UUID, since time.Time) ([]AppCopyState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (copy) copy, ready, restarts FROM core.app_metric_samples
		WHERE app_id = $1 AND at >= $2 ORDER BY copy, at DESC`, appID, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppCopyState{}
	for rows.Next() {
		var c AppCopyState
		if err := rows.Scan(&c.Copy, &c.Ready, &c.Restarts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AppRestartsSince somma i riavvii nuovi di ogni copia da since in poi: gli aumenti fra campioni
// consecutivi della stessa copia. Un pod nuovo parte da zero senza contare come riavvio.
func (s *Store) AppRestartsSince(ctx context.Context, appID uuid.UUID, since time.Time) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		WITH d AS (
			SELECT restarts - LAG(restarts) OVER (PARTITION BY copy ORDER BY at) AS delta
			FROM core.app_metric_samples WHERE app_id = $1 AND at >= $2
		)
		SELECT COALESCE(SUM(delta), 0)::bigint FROM d WHERE delta > 0`, appID, since.UTC()).Scan(&n)
	return n, err
}

// PruneAppMetrics toglie i campioni più vecchi di before. Ritorna quante righe ha tolte.
func (s *Store) PruneAppMetrics(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_metric_samples WHERE at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	// GIT-403: i campioni di richieste ed errori hanno la stessa scadenza.
	tag, err = s.pool.Exec(ctx, `DELETE FROM core.app_traffic_samples WHERE at < $1`, before.UTC())
	if err != nil {
		return n, err
	}
	return n + tag.RowsAffected(), nil
}
