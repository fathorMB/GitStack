package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Uscita delle app (v3-a, GIT-385, H16): le destinazioni private aperte dall'amministratore e la
// restrizione dell'uscita di una singola app.

// ErrAppPrivateDestinationExists: la destinazione (stesso host e porta) è già aperta.
var ErrAppPrivateDestinationExists = errors.New("la destinazione è già aperta")

// AppPrivateDestination è una destinazione privata aperta dall'amministratore.
type AppPrivateDestination struct {
	ID        uuid.UUID
	Host      string
	Port      int
	Note      string
	CreatedBy *AppActor
	CreatedAt time.Time
}

// AppEgressDestination è una voce della restrizione di un'app.
type AppEgressDestination struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// AppEgressRestriction è la restrizione di un'app: Destinations vuoto = l'app non esce da nessuna parte.
type AppEgressRestriction struct {
	Destinations []AppEgressDestination
	UpdatedBy    *AppActor
	UpdatedAt    time.Time
}

// ListAppPrivateDestinations elenca le destinazioni private aperte, dalla più vecchia.
func (s *Store) ListAppPrivateDestinations(ctx context.Context) ([]AppPrivateDestination, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, host, port, note, created_by, created_at FROM core.app_private_destinations ORDER BY created_at, host, port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppPrivateDestination{}
	for rows.Next() {
		var d AppPrivateDestination
		var by []byte
		if err := rows.Scan(&d.ID, &d.Host, &d.Port, &d.Note, &by, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.CreatedBy = decodeActor(by)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReplaceAppPrivateDestinations sostituisce l'elenco intero in una transazione e dà quello nuovo. Le voci
// già presenti (stesso host e porta) mantengono id e autore. Una voce doppia nell'input conta una volta.
func (s *Store) ReplaceAppPrivateDestinations(ctx context.Context, list []AppPrivateDestination, by *AppActor, now time.Time) ([]AppPrivateDestination, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM core.app_private_destinations WHERE NOT ((host, port) IN (SELECT * FROM unnest($1::text[], $2::int[])))`, hosts(list), ports(list)); err != nil {
		return nil, err
	}
	actor, _ := json.Marshal(by)
	if by == nil {
		actor = nil
	}
	for _, d := range list {
		if _, err := tx.Exec(ctx, `INSERT INTO core.app_private_destinations (id, host, port, note, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (host, port) DO UPDATE SET note = EXCLUDED.note`, uuid.New(), d.Host, d.Port, d.Note, actor, now); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil, ErrAppPrivateDestinationExists
			}
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListAppPrivateDestinations(ctx)
}

func hosts(l []AppPrivateDestination) []string {
	out := make([]string, len(l))
	for i, d := range l {
		out[i] = d.Host
	}
	return out
}

func ports(l []AppPrivateDestination) []int32 {
	out := make([]int32, len(l))
	for i, d := range l {
		out[i] = int32(d.Port)
	}
	return out
}

func decodeActor(by []byte) *AppActor {
	if by == nil {
		return nil
	}
	var a AppActor
	if json.Unmarshal(by, &a) != nil {
		return nil
	}
	return &a
}

// GetAppEgressRestriction dà la restrizione dell'app; nil se l'app non ne ha (uscita come l'istanza).
func (s *Store) GetAppEgressRestriction(ctx context.Context, appID uuid.UUID) (*AppEgressRestriction, error) {
	var raw, by []byte
	var r AppEgressRestriction
	err := s.pool.QueryRow(ctx, `SELECT destinations, updated_by, updated_at FROM core.app_egress_restrictions WHERE app_id = $1`, appID).Scan(&raw, &by, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &r.Destinations); err != nil {
		return nil, err
	}
	if r.Destinations == nil {
		r.Destinations = []AppEgressDestination{}
	}
	r.UpdatedBy = decodeActor(by)
	return &r, nil
}

// SetAppEgressRestriction scrive la restrizione dell'app.
func (s *Store) SetAppEgressRestriction(ctx context.Context, appID uuid.UUID, list []AppEgressDestination, by *AppActor, now time.Time) error {
	if list == nil {
		list = []AppEgressDestination{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	var actor []byte
	if by != nil {
		actor, _ = json.Marshal(by)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO core.app_egress_restrictions (app_id, destinations, updated_by, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (app_id) DO UPDATE SET destinations = EXCLUDED.destinations, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`, appID, raw, actor, now)
	return err
}

// DeleteAppEgressRestriction toglie la restrizione: l'app torna all'uscita dell'istanza.
func (s *Store) DeleteAppEgressRestriction(ctx context.Context, appID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM core.app_egress_restrictions WHERE app_id = $1`, appID)
	return err
}
