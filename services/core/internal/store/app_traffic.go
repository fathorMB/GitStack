package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Richieste ed errori 5xx delle app (v3-a, GIT-403, H12). Il controller legge i contatori di Traefik
// e scrive la differenza dall'ultima raccolta: un campione per app e per raccolta. Lo store somma le
// differenze per passo in lettura.

// AppTrafficSample è la differenza di richieste ed errori 5xx fra due raccolte.
type AppTrafficSample struct {
	At        time.Time
	Requests  int64
	Errors5xx int64
}

// AppTrafficBucket è il totale di un passo di tempo.
type AppTrafficBucket struct {
	Time      time.Time
	Requests  int64
	Errors5xx int64
}

// AppendAppTrafficSample scrive il campione di una raccolta.
func (s *Store) AppendAppTrafficSample(ctx context.Context, appID uuid.UUID, m AppTrafficSample) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_traffic_samples (app_id, at, requests, errors_5xx)
		VALUES ($1, $2, $3, $4)`, appID, m.At.UTC().Truncate(time.Microsecond), m.Requests, m.Errors5xx)
	return err
}

// AppTrafficBuckets somma i campioni da since a ora in passi di step secondi, in ordine di tempo.
// Vuoto se nessun campione è arrivato nell'intervallo (sorgente mai risposta).
func (s *Store) AppTrafficBuckets(ctx context.Context, appID uuid.UUID, since time.Time, stepSeconds int) ([]AppTrafficBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT FLOOR(EXTRACT(EPOCH FROM at) / $3)::bigint AS bucket, SUM(requests)::bigint, SUM(errors_5xx)::bigint
		FROM core.app_traffic_samples
		WHERE app_id = $1 AND at >= $2
		GROUP BY bucket
		ORDER BY bucket`, appID, since.UTC(), stepSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppTrafficBucket{}
	for rows.Next() {
		var b int64
		var m AppTrafficBucket
		if err := rows.Scan(&b, &m.Requests, &m.Errors5xx); err != nil {
			return nil, err
		}
		m.Time = time.Unix(b*int64(stepSeconds), 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}
