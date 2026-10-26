package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Log delle app (v3-a, GIT-350, H12). Le righe arrivano già mascherate: il masker dei segreti
// dell'app (appenv.Masker, GIT-348) gira prima di AppendAppLogs, mai dopo. Lo store non decifra
// niente e non conosce i segreti.

// Sorgenti di una riga di log.
const (
	AppLogSourceApp      = "app"
	AppLogSourcePlatform = "platform"
)

// AppLogLine è una riga di log dell'app.
type AppLogLine struct {
	ID      int64
	Time    time.Time
	Source  string
	Copy    *string
	Message string
}

// NewAppLogLine è una riga da scrivere. Copy è nil per gli eventi della piattaforma.
type NewAppLogLine struct {
	Time    time.Time
	Source  string
	Copy    *string
	Message string
}

// AppLogFilter sono i criteri di lettura. Since e Until sono estremi inclusi; Copy e Source vuoti
// non filtrano; Grep è una sottostringa (case sensitive). AfterID legge solo le righe più nuove
// (per la diretta). Limit > 0 prende le N più recenti; 0 le prende tutte, dalla più vecchia.
type AppLogFilter struct {
	Since, Until *time.Time
	Copy         *string
	Source       string
	Grep         string
	AfterID      int64
	Limit        int
}

// AppendAppLogs scrive le righe di un'app in un'unica transazione. Il tempo si porta al microsecondo,
// come lo tiene Postgres: così LastAppLogTime confronta con la stessa precisione.
func (s *Store) AppendAppLogs(ctx context.Context, appID uuid.UUID, lines []NewAppLogLine) error {
	if len(lines) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO core.app_log_lines (app_id, source, copy, at, message)
			VALUES ($1, $2, $3, $4, $5)`, appID, l.Source, l.Copy, l.Time.UTC().Truncate(time.Microsecond), l.Message); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// LastAppLogTime è l'orario dell'ultima riga di una copia (zero se non ce n'è): la diretta riparte da lì.
func (s *Store) LastAppLogTime(ctx context.Context, appID uuid.UUID, copy string) (time.Time, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(at) FROM core.app_log_lines
		WHERE app_id = $1 AND source = 'app' AND copy = $2`, appID, copy).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

// ListAppLogs legge le righe secondo il filtro, in ordine di tempo.
func (s *Store) ListAppLogs(ctx context.Context, appID uuid.UUID, f AppLogFilter) ([]AppLogLine, error) {
	limit := int64(1) << 62
	if f.Limit > 0 {
		limit = int64(f.Limit)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, at, source, copy, message FROM (
			SELECT id, at, source, copy, message FROM core.app_log_lines
			WHERE app_id = $1
			  AND ($2::timestamptz IS NULL OR at >= $2)
			  AND ($3::timestamptz IS NULL OR at <= $3)
			  AND ($4::text IS NULL OR copy = $4)
			  AND ($5::text = '' OR source = $5)
			  AND ($6::text = '' OR strpos(message, $6) > 0)
			  AND id > $7
			ORDER BY at DESC, id DESC
			LIMIT $8
		) latest
		ORDER BY at, id`,
		appID, f.Since, f.Until, f.Copy, f.Source, f.Grep, f.AfterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppLogLine{}
	for rows.Next() {
		var l AppLogLine
		if err := rows.Scan(&l.ID, &l.Time, &l.Source, &l.Copy, &l.Message); err != nil {
			return nil, err
		}
		l.Time = l.Time.UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneAppLogs toglie le righe più vecchie di before e, per ogni app, quelle che eccedono maxBytes
// (si tengono le più recenti). Ritorna quante righe ha tolte.
func (s *Store) PruneAppLogs(ctx context.Context, before time.Time, maxBytes int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_log_lines WHERE at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	removed := tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id, SUM(bytes) OVER (PARTITION BY app_id ORDER BY at DESC, id DESC) AS running
			FROM core.app_log_lines
		)
		DELETE FROM core.app_log_lines WHERE id IN (SELECT id FROM ranked WHERE running > $1)`, maxBytes)
	if err != nil {
		return removed, err
	}
	return removed + tag.RowsAffected(), nil
}
