package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Telemetria dei database gestiti (v3-d, GIT-568, ADR 0003 DB21): campioni di CPU/RAM da
// metrics-server e righe di log di PostgreSQL, conservati come quelli delle app (stessa
// scadenza e stesso tetto di dimensione per database, potatura nel controller). Un campione
// senza CPU ne' memoria vuol dire «sorgente non risponde»: l'API dichiara available=false con
// serie vuote, mai zeri.

// ManagedDBMetricSample è un campione del pod del database.
type ManagedDBMetricSample struct {
	At          time.Time
	Ready       bool
	Restarts    int
	CPUMillis   *int
	MemoryBytes *int64
}

// ManagedDBMetricBucket è la media in un passo di tempo (CPU e memoria nil se assenti).
type ManagedDBMetricBucket struct {
	Time        time.Time
	CPUMillis   *float64
	MemoryBytes *float64
}

// AppendManagedDBMetricSamples scrive i campioni di un database in un'unica transazione.
func (s *Store) AppendManagedDBMetricSamples(ctx context.Context, dbID uuid.UUID, samples []ManagedDBMetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, m := range samples {
		if _, err := tx.Exec(ctx, `INSERT INTO core.managed_db_metric_samples (db_id, at, ready, restarts, cpu_millicores, memory_bytes)
			VALUES ($1, $2, $3, $4, $5, $6)`, dbID, m.At.UTC().Truncate(time.Microsecond), m.Ready, m.Restarts, m.CPUMillis, m.MemoryBytes); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ManagedDBMetricBuckets aggrega i campioni da since a ora in passi di stepSeconds.
func (s *Store) ManagedDBMetricBuckets(ctx context.Context, dbID uuid.UUID, since time.Time, stepSeconds int) ([]ManagedDBMetricBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT FLOOR(EXTRACT(EPOCH FROM at) / $3)::bigint AS bucket,
		       AVG(cpu_millicores)::float8, AVG(memory_bytes)::float8
		FROM core.managed_db_metric_samples
		WHERE db_id = $1 AND at >= $2
		GROUP BY bucket
		ORDER BY bucket`, dbID, since.UTC(), stepSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedDBMetricBucket{}
	for rows.Next() {
		var b int64
		var m ManagedDBMetricBucket
		if err := rows.Scan(&b, &m.CPUMillis, &m.MemoryBytes); err != nil {
			return nil, err
		}
		m.Time = time.Unix(b*int64(stepSeconds), 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// ManagedDBMetricsSourceOK dice se, da since in poi, almeno un campione ha la CPU: metrics-server ha risposto.
func (s *Store) ManagedDBMetricsSourceOK(ctx context.Context, dbID uuid.UUID, since time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.managed_db_metric_samples
		WHERE db_id = $1 AND at >= $2 AND cpu_millicores IS NOT NULL)`, dbID, since.UTC()).Scan(&ok)
	return ok, err
}

// PruneManagedDBMetrics toglie i campioni più vecchi di before. Ritorna quante righe ha tolte.
func (s *Store) PruneManagedDBMetrics(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.managed_db_metric_samples WHERE at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ManagedDBLogLine è una riga di log di PostgreSQL del database.
type ManagedDBLogLine struct {
	ID      int64
	Time    time.Time
	Message string
}

// NewManagedDBLogLine è una riga da scrivere.
type NewManagedDBLogLine struct {
	Time    time.Time
	Message string
}

// ManagedDBLogFilter sono i criteri di lettura. Since e Until sono estremi inclusivi; Grep è una
// sottostringa (case sensitive). AfterID legge solo le righe più nuove (per la diretta).
// Limit > 0 prende le N più recenti; 0 le prende tutte, dalla più vecchia.
type ManagedDBLogFilter struct {
	Since, Until *time.Time
	Grep         string
	AfterID      int64
	Limit        int
}

// AppendManagedDBLogs scrive le righe di un database in un'unica transazione. Il tempo si porta
// al microsecondo, come lo tiene Postgres: così LastManagedDBLogTime confronta con la stessa precisione.
func (s *Store) AppendManagedDBLogs(ctx context.Context, dbID uuid.UUID, lines []NewManagedDBLogLine) error {
	if len(lines) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO core.managed_db_log_lines (db_id, at, message, bytes)
			VALUES ($1, $2, $3, $4)`, dbID, l.Time.UTC().Truncate(time.Microsecond), l.Message, len(l.Message)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// LastManagedDBLogTime è l'orario dell'ultima riga scritta (zero se non ce n'è): la raccolta riparte da lì.
func (s *Store) LastManagedDBLogTime(ctx context.Context, dbID uuid.UUID) (time.Time, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(at) FROM core.managed_db_log_lines WHERE db_id = $1`, dbID).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

// ListManagedDBLogs legge le righe secondo il filtro, in ordine di tempo.
func (s *Store) ListManagedDBLogs(ctx context.Context, dbID uuid.UUID, f ManagedDBLogFilter) ([]ManagedDBLogLine, error) {
	limit := int64(1) << 62
	if f.Limit > 0 {
		limit = int64(f.Limit)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, at, message FROM (
			SELECT id, at, message FROM core.managed_db_log_lines
			WHERE db_id = $1
			  AND ($2::timestamptz IS NULL OR at >= $2)
			  AND ($3::timestamptz IS NULL OR at <= $3)
			  AND ($4::text = '' OR strpos(message, $4) > 0)
			  AND id > $5
			ORDER BY at DESC, id DESC
			LIMIT $6
		) latest
		ORDER BY at, id`, dbID, f.Since, f.Until, f.Grep, f.AfterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedDBLogLine{}
	for rows.Next() {
		var l ManagedDBLogLine
		if err := rows.Scan(&l.ID, &l.Time, &l.Message); err != nil {
			return nil, err
		}
		l.Time = l.Time.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneManagedDBLogs toglie le righe più vecchie di before e, per ogni database, quelle che
// eccedono maxBytes (si tengono le più recenti). Ritorna quante righe ha tolte.
func (s *Store) PruneManagedDBLogs(ctx context.Context, before time.Time, maxBytes int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.managed_db_log_lines WHERE at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	removed := tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id, SUM(bytes) OVER (PARTITION BY db_id ORDER BY at DESC, id DESC) AS running
			FROM core.managed_db_log_lines
		)
		DELETE FROM core.managed_db_log_lines WHERE id IN (SELECT id FROM ranked WHERE running > $1)`, maxBytes)
	if err != nil {
		return removed, err
	}
	return removed + tag.RowsAffected(), nil
}
