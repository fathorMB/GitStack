package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppSealer cifra e decifra i segreti delle app (webhooks.Keyring lo implementa: stessa chiave e
// stesso formato dei segreti dei webhook e della CI, ma righe in tabelle proprie). L'id della riga
// è il dato autenticato.
type AppSealer interface {
	Seal(rowID uuid.UUID, secret string) (ciphertext, nonce []byte, keyID string, err error)
	Open(rowID uuid.UUID, ciphertext, nonce []byte, keyID string) (string, error)
}

// ListAppSecrets elenca i metadati dei segreti dell'app per nome. Non legge mai ciphertext.
func (s *Store) ListAppSecrets(ctx context.Context, appID uuid.UUID) ([]AppSecretMeta, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, version, updated_at, updated_by FROM core.app_secrets
		WHERE app_id = $1 ORDER BY name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppSecretMeta{}
	for rows.Next() {
		var m AppSecretMeta
		var by []byte
		if err := rows.Scan(&m.Name, &m.Version, &m.UpdatedAt, &by); err != nil {
			return nil, err
		}
		if m.UpdatedBy, err = scanActor(by); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PutAppSecret crea o sostituisce un segreto (cifrato) e crea la versione di configurazione con il
// suo deploy. Il valore non torna al chiamante. ErrEnvNameInUse se il nome è già una variabile.
func (s *Store) PutAppSecret(ctx context.Context, keys AppSealer, appID uuid.UUID, name, value string, actor *AppActor, now time.Time) (AppSecretMeta, AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockApp(ctx, tx, appID); err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	if taken, err := nameTakenByOther(ctx, tx, appID, name, true); err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	} else if taken {
		return AppSecretMeta{}, AppVersion{}, ErrEnvNameInUse
	}
	// Il segreto esistente tiene il suo id (dato autenticato): si ricifra con quello.
	id, found := uuid.New(), true
	err = tx.QueryRow(ctx, `SELECT id FROM core.app_secrets WHERE app_id = $1 AND name = $2 FOR UPDATE`, appID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		found = false
	} else if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	ct, nonce, keyID, err := keys.Seal(id, value)
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	if found {
		_, err = tx.Exec(ctx, `UPDATE core.app_secrets SET ciphertext = $2, nonce = $3, secret_key_id = $4, version = $5,
			updated_by = $6, updated_at = $7 WHERE id = $1`, id, ct, nonce, keyID, number, actorJSON(actor), now)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO core.app_secrets (id, app_id, name, ciphertext, nonce, secret_key_id, version, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, appID, name, ct, nonce, keyID, number, actorJSON(actor), now)
	}
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, "segreto "+name, actor, now)
	if err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppSecretMeta{}, AppVersion{}, err
	}
	return AppSecretMeta{Name: name, Version: number, UpdatedAt: now, UpdatedBy: actor}, ver, nil
}

// DeleteAppSecret elimina un segreto e crea la versione di configurazione. ErrNotFound se non c'è.
func (s *Store) DeleteAppSecret(ctx context.Context, appID uuid.UUID, name string, actor *AppActor, now time.Time) (AppVersion, error) {
	return s.removeAppEnv(ctx, appID, name, true, actor, now)
}

// AppSecretValues decifra i segreti dell'app. Non la espone nessuna API: serve al controller dei
// pod (deploy) e al mascheramento dei log dell'app. Un segreto che non si decifra è un errore.
func (s *Store) AppSecretValues(ctx context.Context, keys AppSealer, appID uuid.UUID) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, ciphertext, nonce, secret_key_id FROM core.app_secrets WHERE app_id = $1`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id uuid.UUID
		var name, keyID string
		var ct, nonce []byte
		if err := rows.Scan(&id, &name, &ct, &nonce, &keyID); err != nil {
			return nil, err
		}
		v, err := keys.Open(id, ct, nonce, keyID)
		if err != nil {
			return nil, errors.New("segreto " + name + " non decifrabile: " + err.Error())
		}
		out[name] = v
	}
	return out, rows.Err()
}
