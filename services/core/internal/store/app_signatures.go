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

// Firme al deploy (v3-a, GIT-349, H10): l'impostazione «solo immagini firmate» e le chiavi cosign
// fidate aggiuntive. La chiave della CI dell'istanza non è qui: è del servizio git.

// ErrAppTrustedKeyExists: la chiave (stessa impronta) è già fidata.
var ErrAppTrustedKeyExists = errors.New("la chiave è già fidata")

// AppTrustedKey è una chiave cosign fidata aggiunta dall'amministratore.
type AppTrustedKey struct {
	ID          uuid.UUID
	Name        string
	Fingerprint string
	PublicKey   string
	CreatedBy   *AppActor
	CreatedAt   time.Time
}

// AppSignedImagesOnly dice se l'istanza accetta solo immagini firmate (H10).
func (s *Store) AppSignedImagesOnly(ctx context.Context) (bool, error) {
	var on bool
	err := s.pool.QueryRow(ctx, `SELECT signed_images_only FROM core.app_capacity_settings WHERE singleton`).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetAppSignedImagesOnly accende o spegne «solo immagini firmate».
func (s *Store) SetAppSignedImagesOnly(ctx context.Context, on bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_capacity_settings (singleton, signed_images_only, updated_at) VALUES (true, $1, now())
		ON CONFLICT (singleton) DO UPDATE SET signed_images_only = EXCLUDED.signed_images_only, updated_at = now()`, on)
	return err
}

// ListAppTrustedKeys elenca le chiavi fidate aggiuntive, dalla più vecchia.
func (s *Store) ListAppTrustedKeys(ctx context.Context) ([]AppTrustedKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, fingerprint, public_key, created_by, created_at FROM core.app_trusted_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppTrustedKey{}
	for rows.Next() {
		var k AppTrustedKey
		var by []byte
		if err := rows.Scan(&k.ID, &k.Name, &k.Fingerprint, &k.PublicKey, &by, &k.CreatedAt); err != nil {
			return nil, err
		}
		if by != nil {
			var a AppActor
			if json.Unmarshal(by, &a) == nil {
				k.CreatedBy = &a
			}
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddAppTrustedKey salva una chiave fidata; ErrAppTrustedKeyExists se l'impronta c'è già.
func (s *Store) AddAppTrustedKey(ctx context.Context, k AppTrustedKey) (AppTrustedKey, error) {
	var by []byte
	if k.CreatedBy != nil {
		by, _ = json.Marshal(k.CreatedBy)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_trusted_keys (id, name, fingerprint, public_key, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		k.ID, k.Name, k.Fingerprint, k.PublicKey, by, k.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return AppTrustedKey{}, ErrAppTrustedKeyExists
	}
	return k, err
}

// DeleteAppTrustedKey toglie una chiave fidata; ErrNotFound se non c'è.
func (s *Store) DeleteAppTrustedKey(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_trusted_keys WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
