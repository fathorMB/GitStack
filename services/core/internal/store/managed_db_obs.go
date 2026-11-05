package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Metriche e log dei database gestiti (DB21): campioni e righe stanno nel database di core, quindi
// sopravvivono alla sostituzione del pod. Retention come le app: 7 giorni.

// ManagedDBSample e' un campione; Cpu/Mem nil = metrics-server non ha risposto, DBBytes nil = non letto.
type ManagedDBSample struct {
	At          time.Time
	CPUMillis   *int
	MemoryBytes *int64
	DBBytes     *int64
}

// AppendManagedDBSample scrive un campione (idempotente sull'istante).
func (s *Store) AppendManagedDBSample(ctx context.Context, id uuid.UUID, m ManagedDBSample) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO core.managed_db_metrics (db_id, at, cpu_millis, memory_bytes, db_bytes) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (db_id, at) DO NOTHING`, id, m.At, m.CPUMillis, m.MemoryBytes, m.DBBytes)
	return err
}

// ManagedDBBucket e' la media di un intervallo; i campi nil non hanno campioni.
type ManagedDBBucket struct {
	At          time.Time
	CPUMillis   *float64
	MemoryBytes *float64
	DBBytes     *float64
}

// ManagedDBBuckets raggruppa i campioni da `since` in intervalli di stepSeconds.
func (s *Store) ManagedDBBuckets(ctx context.Context, id uuid.UUID, since time.Time, stepSeconds int) ([]ManagedDBBucket, error) {
	rows, err := s.pool.Query(ctx, `SELECT to_timestamp(floor(extract(epoch FROM at) / $3) * $3) AS b,
			avg(cpu_millis)::float8, avg(memory_bytes)::float8, avg(db_bytes)::float8
		FROM core.managed_db_metrics WHERE db_id = $1 AND at >= $2 GROUP BY b ORDER BY b`, id, since, stepSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBBucket
	for rows.Next() {
		var b ManagedDBBucket
		if err := rows.Scan(&b.At, &b.CPUMillis, &b.MemoryBytes, &b.DBBytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PurgeManagedDBObservability toglie campioni e log oltre la retention.
func (s *Store) PurgeManagedDBObservability(ctx context.Context, before time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM core.managed_db_metrics WHERE at < $1`, before); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM core.managed_db_logs WHERE at < $1`, before)
	return err
}

// ManagedDBLogLine e' una riga di log conservata.
type ManagedDBLogLine struct {
	ID      int64
	At      time.Time
	Message string
}

// LastManagedDBLogTime ritorna l'istante dell'ultima riga conservata (zero se non ce ne sono).
func (s *Store) LastManagedDBLogTime(ctx context.Context, id uuid.UUID) (time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(at) FROM core.managed_db_logs WHERE db_id = $1`, id).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// AppendManagedDBLogs scrive le righe nuove.
func (s *Store) AppendManagedDBLogs(ctx context.Context, id uuid.UUID, lines []ManagedDBLogLine) error {
	for _, l := range lines {
		if _, err := s.pool.Exec(ctx, `INSERT INTO core.managed_db_logs (db_id, at, message) VALUES ($1,$2,$3)`, id, l.At, l.Message); err != nil {
			return err
		}
	}
	return nil
}

// ManagedDBLogFilter filtra la lettura; Limit = le piu' recenti; AfterID per il follow.
type ManagedDBLogFilter struct {
	Since, Until time.Time
	Grep         string
	Limit        int
	AfterID      int64
}

// ListManagedDBLogs ritorna le righe in ordine cronologico.
func (s *Store) ListManagedDBLogs(ctx context.Context, id uuid.UUID, f ManagedDBLogFilter) ([]ManagedDBLogLine, error) {
	if f.Limit <= 0 {
		f.Limit = 1000
	}
	q := `SELECT id, at, message FROM (SELECT id, at, message FROM core.managed_db_logs WHERE db_id = $1 AND id > $2
		AND ($3::timestamptz IS NULL OR at >= $3) AND ($4::timestamptz IS NULL OR at <= $4)
		AND ($5 = '' OR position(lower($5) in lower(message)) > 0) ORDER BY id DESC LIMIT $6) x ORDER BY id`
	var since, until any
	if !f.Since.IsZero() {
		since = f.Since
	}
	if !f.Until.IsZero() {
		until = f.Until
	}
	rows, err := s.pool.Query(ctx, q, id, f.AfterID, since, until, strings.TrimSpace(f.Grep), f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBLogLine
	for rows.Next() {
		var l ManagedDBLogLine
		if err := rows.Scan(&l.ID, &l.At, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
