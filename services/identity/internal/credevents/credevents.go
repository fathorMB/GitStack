// Package credevents registra gli avvisi sulle credenziali di un utente (F13,
// ASVS V2.2.3, V2.3.3, V2.5.5): cambio password, reset da parte di un
// amministratore, nuovo token personale, nuova chiave SSH e token in
// scadenza. Identity non manda email né notifiche: scrive qui l'evento e core
// lo legge da GET /internal/credential-events (consegna almeno una volta, con
// conferma esplicita).
package credevents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tipi di evento.
const (
	PasswordChanged = "password_changed" // l'utente ha cambiato la propria password
	PasswordReset   = "password_reset"   // un amministratore l'ha reimpostata
	TokenCreated    = "token_created"
	TokenExpiring   = "token_expiring"
	SSHKeyAdded     = "ssh_key_added"
)

// DefaultExpiryWindow: quanto prima della scadenza si avvisa per un token.
const DefaultExpiryWindow = 7 * 24 * time.Hour

// Event è un evento da consegnare.
type Event struct {
	ID        int64          `json:"id"`
	UserID    uuid.UUID      `json:"userId"`
	Kind      string         `json:"kind"`
	Detail    map[string]any `json:"detail"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Service legge e scrive gli eventi.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// ExpiryWindow: anticipo dell'avviso di scadenza; 0 = DefaultExpiryWindow.
	ExpiryWindow time.Duration
}

// New crea il servizio; now nil = time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

// Record scrive un evento per l'utente. detail non deve contenere segreti.
func (s *Service) Record(ctx context.Context, userID uuid.UUID, kind string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("credevents: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO identity.credential_events (user_id, kind, detail, created_at)
		VALUES ($1, $2, $3, $4)`, userID, kind, b, s.now()); err != nil {
		return fmt.Errorf("credevents: scrittura dell'evento: %w", err)
	}
	return nil
}

// ScanExpiring scrive un evento token_expiring per ogni token non revocato che
// scade entro la finestra e non è ancora stato avvisato, e lo segna. Ritorna
// quanti ne ha trovati. È idempotente (un avviso per token).
func (s *Service) ScanExpiring(ctx context.Context) (int, error) {
	win := s.ExpiryWindow
	if win <= 0 {
		win = DefaultExpiryWindow
	}
	now := s.now()
	tag, err := s.pool.Exec(ctx, `WITH due AS (
			UPDATE identity.api_tokens SET expiry_notified_at = $1
			WHERE revoked_at IS NULL AND expiry_notified_at IS NULL
			  AND expires_at IS NOT NULL AND expires_at > $1 AND expires_at <= $2
			RETURNING user_id, name, token_hint, expires_at)
		INSERT INTO identity.credential_events (user_id, kind, detail, created_at)
		SELECT user_id, 'token_expiring',
			jsonb_build_object('name', name, 'hint', token_hint, 'expiresAt', expires_at), $1
		FROM due`, now, now.Add(win))
	if err != nil {
		return 0, fmt.Errorf("credevents: scansione delle scadenze: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Pending elenca fino a limit eventi non ancora consegnati, dal più vecchio.
func (s *Service) Pending(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, kind, detail, created_at FROM identity.credential_events
		WHERE delivered_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("credevents: lettura: %w", err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var raw []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &e.Detail); err != nil {
			return nil, fmt.Errorf("credevents: dettaglio non valido: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ack segna come consegnati esattamente gli eventi con quegli id. Non una
// soglia: gli id di una sequence non seguono l'ordine di commit, quindi un
// evento con id minore committato dopo la lettura di Pending non va confermato
// per sbaglio. Ritorna quanti ne ha segnati.
func (s *Service) Ack(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE identity.credential_events SET delivered_at = $2
		WHERE id = ANY($1) AND delivered_at IS NULL`, ids, s.now())
	if err != nil {
		return 0, fmt.Errorf("credevents: conferma: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Purge elimina gli eventi consegnati prima di before.
func (s *Service) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM identity.credential_events WHERE delivered_at IS NOT NULL AND delivered_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
