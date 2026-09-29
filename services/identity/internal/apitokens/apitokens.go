// Package apitokens gestisce i token personali (gst_...) sulla tabella
// identity.api_tokens: creazione con scope e scadenza obbligatoria, elenco,
// revoca e verifica per il gateway.
//
// Nel database c'è solo l'hash SHA-256 grezzo (tokens.HashBytes, BYTEA di 32
// byte, mai l'hex) e un token_hint di 4 caratteri. Il valore in chiaro esiste
// solo nel risultato di Create: mai nei log, mai negli errori (nessun errore
// di questo pacchetto lo contiene).
package apitokens

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/tokens"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultMaxLifetime è il massimo di durata di un token se non configurato.
const DefaultMaxLifetime = 365 * 24 * time.Hour

// touchInterval: last_used_at si riscrive al più una volta al minuto.
const touchInterval = time.Minute

// ErrNotFound: token sconosciuto (o di un altro utente) per la revoca.
var ErrNotFound = errors.New("token non trovato")

// ErrInactive: token sconosciuto, scaduto, revocato o di utente disattivato
// (indistinguibili di proposito).
var ErrInactive = errors.New("token non attivo")

// ValidationError elenca i campi non validi (422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "dati del token non validi" }

// NameInUseError: l'utente ha già un token con quel nome (409).
type NameInUseError struct{}

func (e *NameInUseError) Error() string { return "nome del token già in uso" }

// Token è una riga di identity.api_tokens senza l'hash.
type Token struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	Hint       string
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Principal è l'esito di una verifica riuscita.
type Principal struct {
	Token    Token
	Username string
	Kind     string
	IsAdmin  bool
}

// Service è il repository dei token personali.
type Service struct {
	pool        *pgxpool.Pool
	now         func() time.Time
	maxLifetime time.Duration
}

// New crea il servizio. now nil usa time.Now; maxLifetime <= 0 usa
// DefaultMaxLifetime (la lettura dell'ambiente è di chi monta il server).
func New(pool *pgxpool.Pool, now func() time.Time, maxLifetime time.Duration) *Service {
	if now == nil {
		now = time.Now
	}
	if maxLifetime <= 0 {
		maxLifetime = DefaultMaxLifetime
	}
	return &Service{pool: pool, now: now, maxLifetime: maxLifetime}
}

// MaxLifetime è la durata massima concessa a un token.
func (s *Service) MaxLifetime() time.Duration { return s.maxLifetime }

// CreateInput sono i dati per creare un token. ExpiresAt è obbligatoria.
type CreateInput struct {
	UserID    uuid.UUID
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

const columns = `id, user_id, name, token_hint, scopes, created_at, expires_at, last_used_at, revoked_at`

func scan(row pgx.Row) (Token, error) {
	var t Token
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hint, &t.Scopes, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

// Create valida e salva un nuovo token. Ritorna il valore in chiaro (da
// mostrare una sola volta) e la riga.
func (s *Service) Create(ctx context.Context, in CreateInput) (string, Token, error) {
	now := s.now()
	f := map[string]string{}
	if n := len([]rune(in.Name)); n < 1 || n > 64 {
		f["name"] = "da 1 a 64 caratteri"
	}
	scopes, err := tokens.ParseScopes(in.Scopes)
	switch {
	case len(in.Scopes) == 0:
		f["scopes"] = "almeno uno scope"
	case err != nil:
		f["scopes"] = "scope sconosciuto o duplicato"
	}
	switch {
	case in.ExpiresAt == nil:
		f["expiresAt"] = "obbligatoria"
	case !in.ExpiresAt.After(now):
		f["expiresAt"] = "deve essere nel futuro"
	case in.ExpiresAt.After(now.Add(s.maxLifetime)):
		f["expiresAt"] = fmt.Sprintf("oltre il massimo consentito (%d giorni)", int(s.maxLifetime/(24*time.Hour)))
	}
	if len(f) > 0 {
		return "", Token{}, &ValidationError{Fields: f}
	}

	plain, err := tokens.Generate()
	if err != nil {
		return "", Token{}, fmt.Errorf("generazione del token non riuscita: %w", err)
	}
	h := tokens.HashBytes(plain)
	id := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", Token{}, fmt.Errorf("creazione del token non riuscita: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Un token revocato non compare più nell'elenco ma il suo nome è
	// nell'indice (utente, nome): lo si libera per poterlo riusare.
	if _, err := tx.Exec(ctx, `DELETE FROM identity.api_tokens WHERE user_id = $1 AND name = $2 AND revoked_at IS NOT NULL`, in.UserID, in.Name); err != nil {
		return "", Token{}, fmt.Errorf("creazione del token non riuscita: %w", err)
	}
	t, err := scan(tx.QueryRow(ctx, `
		INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+columns,
		id, in.UserID, in.Name, h[:], plain[len(plain)-4:], scopes, now, *in.ExpiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return "", Token{}, &NameInUseError{}
	}
	if err != nil {
		// Solo il messaggio del driver: nessun parametro (contiene l'hash).
		return "", Token{}, fmt.Errorf("creazione del token non riuscita: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", Token{}, fmt.Errorf("creazione del token non riuscita: %w", err)
	}
	return plain, t, nil
}

// List ritorna i token non revocati dell'utente (anche scaduti: servono a
// riconoscerli), dal più recente, con il totale.
func (s *Service) List(ctx context.Context, userID uuid.UUID, page, perPage int) ([]Token, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM identity.api_tokens WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("conteggio dei token non riuscito: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM identity.api_tokens
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC, id LIMIT $2 OFFSET $3`,
		userID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco dei token non riuscito: %w", err)
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("lettura di un token non riuscita: %w", err)
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// Revoke revoca un token dell'utente. Un token già revocato o di un altro
// utente è ErrNotFound.
func (s *Service) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.api_tokens SET revoked_at = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID, s.now())
	if err != nil {
		return fmt.Errorf("revoca del token non riuscita: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Verify risolve il valore di un token in un Principal. Formato errato,
// sconosciuto, scaduto, revocato o di utente disattivato: ErrInactive.
// Aggiorna last_used_at al più una volta al minuto.
func (s *Service) Verify(ctx context.Context, plain string) (Principal, error) {
	if !tokens.Validate(plain) {
		return Principal{}, ErrInactive
	}
	h := tokens.HashBytes(plain)
	now := s.now()
	var p Principal
	var stored []byte
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.user_id, t.name, t.token_hint, t.scopes, t.created_at, t.expires_at, t.last_used_at, t.revoked_at,
		       t.token_hash, u.username, u.kind, u.is_admin
		FROM identity.api_tokens t JOIN identity.users u ON u.id = t.user_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > $2) AND u.is_active`, h[:], now).
		Scan(&p.Token.ID, &p.Token.UserID, &p.Token.Name, &p.Token.Hint, &p.Token.Scopes, &p.Token.CreatedAt,
			&p.Token.ExpiresAt, &p.Token.LastUsedAt, &p.Token.RevokedAt, &stored, &p.Username, &p.Kind, &p.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInactive
	}
	if err != nil {
		return Principal{}, fmt.Errorf("verifica del token non riuscita: %w", err)
	}
	if !tokens.CompareToken(plain, stored) {
		return Principal{}, ErrInactive
	}
	if p.Token.LastUsedAt == nil || now.Sub(*p.Token.LastUsedAt) >= touchInterval {
		if _, err := s.pool.Exec(ctx, `UPDATE identity.api_tokens SET last_used_at = $2 WHERE id = $1`, p.Token.ID, now); err == nil {
			p.Token.LastUsedAt = &now
		}
	}
	return p, nil
}
