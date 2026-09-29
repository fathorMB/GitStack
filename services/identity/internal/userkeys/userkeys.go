// Package userkeys gestisce le chiavi SSH pubbliche degli utenti sulla
// tabella identity.ssh_keys. Il parsing e il fingerprint SHA256 sono di
// sshkeys.Parse; l'unicità del fingerprint in tutta l'installazione la
// garantisce l'indice ssh_keys_fingerprint_key, dalla cui violazione nasce
// FingerprintInUseError (409 ssh_key_in_use).
package userkeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/sshkeys"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// touchInterval: last_used_at si riscrive al più una volta al minuto.
const touchInterval = time.Minute

// ErrNotFound: chiave sconosciuta (o di un altro utente).
var ErrNotFound = errors.New("chiave SSH non trovata")

// ValidationError elenca i campi non validi (422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "dati della chiave SSH non validi" }

// FingerprintInUseError: la chiave è già registrata (da questo o da un
// altro utente) (409 ssh_key_in_use).
type FingerprintInUseError struct{}

func (e *FingerprintInUseError) Error() string { return "chiave SSH già registrata" }

// TitleInUseError: l'utente ha già una chiave con quel titolo (409
// already_exists, campo title).
type TitleInUseError struct{}

func (e *TitleInUseError) Error() string { return "titolo della chiave già in uso" }

// Key è una riga di identity.ssh_keys.
type Key struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Title       string
	KeyType     string
	PublicKey   string
	Fingerprint string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

// Service è il repository delle chiavi SSH.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New crea il servizio. now nil usa time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

const columns = `id, user_id, title, key_type, public_key, fingerprint_sha256, created_at, last_used_at`

func scan(row pgx.Row) (Key, error) {
	var k Key
	err := row.Scan(&k.ID, &k.UserID, &k.Title, &k.KeyType, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.LastUsedAt)
	return k, err
}

// Add valida e salva una chiave dell'utente.
func (s *Service) Add(ctx context.Context, userID uuid.UUID, title, publicKey string) (Key, error) {
	f := map[string]string{}
	if n := len([]rune(title)); n < 1 || n > 64 {
		f["title"] = "da 1 a 64 caratteri"
	}
	parsed, err := sshkeys.Parse(publicKey)
	if err != nil {
		f["publicKey"] = err.Error()
	}
	if len(f) > 0 {
		return Key{}, &ValidationError{Fields: f}
	}
	k, err := scan(s.pool.QueryRow(ctx, `
		INSERT INTO identity.ssh_keys (id, user_id, title, key_type, public_key, fingerprint_sha256, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+columns,
		uuid.New(), userID, title, parsed.Type, publicKey, parsed.Fingerprint, s.now()))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "ssh_keys_fingerprint_key":
			return Key{}, &FingerprintInUseError{}
		case "ssh_keys_user_title_key":
			return Key{}, &TitleInUseError{}
		}
	}
	if err != nil {
		return Key{}, fmt.Errorf("aggiunta della chiave SSH non riuscita: %w", err)
	}
	return k, nil
}

// List ritorna le chiavi dell'utente, dalla più recente, con il totale.
func (s *Service) List(ctx context.Context, userID uuid.UUID, page, perPage int) ([]Key, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM identity.ssh_keys WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("conteggio delle chiavi SSH non riuscito: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM identity.ssh_keys WHERE user_id = $1
		ORDER BY created_at DESC, id LIMIT $2 OFFSET $3`, userID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco delle chiavi SSH non riuscito: %w", err)
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("lettura di una chiave SSH non riuscita: %w", err)
		}
		out = append(out, k)
	}
	return out, total, rows.Err()
}

// Get legge una chiave dell'utente; di un altro utente è ErrNotFound.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Key, error) {
	k, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM identity.ssh_keys WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, fmt.Errorf("lettura della chiave SSH non riuscita: %w", err)
	}
	return k, nil
}

// Delete elimina una chiave dell'utente; di un altro utente è ErrNotFound.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM identity.ssh_keys WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("eliminazione della chiave SSH non riuscita: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Lookup risolve un fingerprint nella chiave di un utente attivo (per il
// servizio git all'accesso SSH) e aggiorna last_used_at al più una volta al
// minuto. Sconosciuto o utente disattivato: ErrNotFound.
func (s *Service) Lookup(ctx context.Context, fingerprint string) (Key, error) {
	now := s.now()
	k, err := scan(s.pool.QueryRow(ctx, `
		SELECT k.id, k.user_id, k.title, k.key_type, k.public_key, k.fingerprint_sha256, k.created_at, k.last_used_at
		FROM identity.ssh_keys k JOIN identity.users u ON u.id = k.user_id
		WHERE k.fingerprint_sha256 = $1 AND u.is_active`, fingerprint))
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, fmt.Errorf("ricerca della chiave SSH non riuscita: %w", err)
	}
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) >= touchInterval {
		if _, err := s.pool.Exec(ctx, `UPDATE identity.ssh_keys SET last_used_at = $2 WHERE id = $1`, k.ID, now); err == nil {
			k.LastUsedAt = &now
		}
	}
	return k, nil
}
