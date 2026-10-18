// Package sessions gestisce le sessioni web (cookie gst_session) sulla
// tabella identity.sessions.
//
// Il valore del cookie sono 32 byte casuali (crypto/rand) in base64url; nel
// database c'è solo il suo SHA-256 grezzo (tokens.HashBytes, 32 byte): il
// valore in chiaro esiste solo nella risposta di Create e nel cookie del
// client, mai nei log né nel database. La scadenza è assoluta (TTL fisso dalla
// creazione) più un idle timeout opzionale su last_seen_at, aggiornato al più
// una volta al minuto.
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/tokens"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultTTL è la durata assoluta di una sessione (7 giorni).
const DefaultTTL = 7 * 24 * time.Hour

// DefaultIdleTimeout è l'inattività dopo la quale una sessione non vale più
// (24 ore): last_seen_at si aggiorna a ogni uso, al più una volta al minuto.
const DefaultIdleTimeout = 24 * time.Hour

// touchInterval: last_seen_at si riscrive solo se più vecchio di così.
const touchInterval = time.Minute

// ErrNotFound: sessione sconosciuta, scaduta o revocata (indistinguibili).
var ErrNotFound = errors.New("sessione non trovata")

// Session è una riga di identity.sessions (senza l'hash).
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	AuthMethod string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Store è il repository delle sessioni.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
	ttl  time.Duration
	idle time.Duration
}

// New crea lo Store. now nil usa time.Now; ttl <= 0 usa DefaultTTL.
func New(pool *pgxpool.Pool, now func() time.Time, ttl time.Duration) *Store {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{pool: pool, now: now, ttl: ttl}
}

// SetIdleTimeout imposta l'inattività massima di una sessione; d <= 0 la
// disattiva (resta solo la scadenza assoluta). Da chiamare prima di servire.
func (s *Store) SetIdleTimeout(d time.Duration) { s.idle = d }

// IdleTimeout è l'inattività massima configurata (0: nessuna).
func (s *Store) IdleTimeout() time.Duration { return s.idle }

// TTL è la durata assoluta delle sessioni create da questo Store.
func (s *Store) TTL() time.Duration { return s.ttl }

func hashValue(value string) []byte {
	h := tokens.HashBytes(value)
	return h[:]
}

// Create apre una sessione per userID e ritorna il valore del cookie (in
// chiaro, da usare una sola volta) e la sessione. authMethod è 'password' o
// 'oidc'; ip può essere vuoto o non valido (salvato come NULL).
func (s *Store) Create(ctx context.Context, userID uuid.UUID, authMethod, userAgent, ip string) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, fmt.Errorf("generazione del valore di sessione non riuscita: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)

	var addr *netip.Addr
	if a, err := netip.ParseAddr(ip); err == nil {
		a = a.Unmap()
		addr = &a
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}

	now := s.now()
	sess := Session{
		ID: uuid.New(), UserID: userID, AuthMethod: authMethod,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.ttl),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.sessions (id, user_id, token_hash, auth_method, user_agent, ip_address, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)`,
		sess.ID, userID, hashValue(value), authMethod, userAgent, addr, now, sess.ExpiresAt)
	if err != nil {
		return "", Session{}, fmt.Errorf("creazione della sessione non riuscita: %w", err)
	}
	return value, sess, nil
}

// Lookup risolve il valore di un cookie in una sessione valida (non scaduta,
// non revocata) di un utente attivo. Altrimenti ErrNotFound.
func (s *Store) Lookup(ctx context.Context, value string) (Session, error) {
	if value == "" {
		return Session{}, ErrNotFound
	}
	now := s.now()
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.auth_method, s.created_at, s.last_seen_at, s.expires_at
		FROM identity.sessions s JOIN identity.users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND u.is_active
		  AND ($3::timestamptz IS NULL OR s.last_seen_at > $3::timestamptz)`,
		hashValue(value), now, s.idleCutoff(now)).
		Scan(&sess.ID, &sess.UserID, &sess.AuthMethod, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("lettura della sessione non riuscita: %w", err)
	}
	if now.Sub(sess.LastSeenAt) >= touchInterval {
		if _, err := s.pool.Exec(ctx, `UPDATE identity.sessions SET last_seen_at = $2 WHERE id = $1`, sess.ID, now); err == nil {
			sess.LastSeenAt = now
		}
	}
	return sess, nil
}

// idleCutoff è l'istante prima del quale una sessione è inattiva (nil: nessun
// idle timeout).
func (s *Store) idleCutoff(now time.Time) *time.Time {
	if s.idle <= 0 {
		return nil
	}
	c := now.Add(-s.idle)
	return &c
}

// Info è una sessione attiva come la vede il suo proprietario.
type Info struct {
	Session
	UserAgent string
	IPAddress string
}

// ListForUser elenca le sessioni attive (non scadute, non revocate, non
// inattive) dell'utente, dalla più recente.
func (s *Store) ListForUser(ctx context.Context, userID uuid.UUID) ([]Info, error) {
	now := s.now()
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, auth_method, created_at, last_seen_at, expires_at, user_agent, COALESCE(host(ip_address), '')
		FROM identity.sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2
		  AND ($3::timestamptz IS NULL OR last_seen_at > $3::timestamptz)
		ORDER BY last_seen_at DESC, id`, userID, now, s.idleCutoff(now))
	if err != nil {
		return nil, fmt.Errorf("elenco delle sessioni non riuscito: %w", err)
	}
	defer rows.Close()
	var out []Info
	for rows.Next() {
		var i Info
		if err := rows.Scan(&i.ID, &i.UserID, &i.AuthMethod, &i.CreatedAt, &i.LastSeenAt, &i.ExpiresAt, &i.UserAgent, &i.IPAddress); err != nil {
			return nil, fmt.Errorf("lettura delle sessioni non riuscita: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeByID revoca la sessione id se appartiene a userID (e non è già
// revocata o scaduta). Ritorna ErrNotFound altrimenti: una sessione altrui è
// indistinguibile da una inesistente.
func (s *Store) RevokeByID(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.sessions SET revoked_at = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL AND expires_at > $3`, id, userID, s.now())
	if err != nil {
		return fmt.Errorf("revoca della sessione non riuscita: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Revoke revoca la sessione del cookie. Ritorna true se ne ha revocata una.
func (s *Store) Revoke(ctx context.Context, value string) (bool, error) {
	if value == "" {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.sessions SET revoked_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2`,
		hashValue(value), s.now())
	if err != nil {
		return false, fmt.Errorf("revoca della sessione non riuscita: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeAllForUser revoca tutte le sessioni attive dell'utente, salvo
// (se non nil) except. Ritorna quante ne ha revocate.
func (s *Store) RevokeAllForUser(ctx context.Context, userID uuid.UUID, except *uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, RevokeAllSQL, userID, s.now(), except)
	if err != nil {
		return 0, fmt.Errorf("revoca delle sessioni dell'utente non riuscita: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RevokeAllSQL è la query di revoca di massa ($1 utente, $2 istante, $3
// sessione da risparmiare o NULL); la usa anche users.Service dentro le sue
// transazioni (cambio password, disattivazione).
const RevokeAllSQL = `
	UPDATE identity.sessions SET revoked_at = $2
	WHERE user_id = $1 AND revoked_at IS NULL AND ($3::uuid IS NULL OR id <> $3::uuid)`

// DeleteExpired elimina le sessioni scadute o revocate da più di olderThan.
func (s *Store) DeleteExpired(ctx context.Context, olderThan time.Duration) (int64, error) {
	cut := s.now().Add(-olderThan)
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM identity.sessions WHERE expires_at < $1 OR (revoked_at IS NOT NULL AND revoked_at < $1)`, cut)
	if err != nil {
		return 0, fmt.Errorf("pulizia delle sessioni non riuscita: %w", err)
	}
	return tag.RowsAffected(), nil
}
