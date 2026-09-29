package oidc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProviderNotFound: slug sconosciuto o provider non abilitato.
var ErrProviderNotFound = errors.New("provider OIDC non trovato")

// ErrIdentityExists: l'identità (provider, subject) è già collegata (corsa
// fra due callback).
var ErrIdentityExists = errors.New("identità OIDC già collegata")

// ProviderRow è una riga abilitata di identity.oidc_providers.
type ProviderRow struct {
	ID        uuid.UUID
	Slug      string
	Issuer    string
	ClientID  string
	SecretEnc []byte
	EncKeyID  string
	Scopes    []string
}

// SyncRow è un provider da scrivere in identity.oidc_providers.
type SyncRow struct {
	Slug        string
	DisplayName string
	Issuer      string
	ClientID    string
	SecretEnc   []byte
	EncKeyID    string
	Scopes      []string
}

// Repository è ciò che il flusso chiede al database. PGStore lo implementa
// su Postgres; i test unitari usano un finto.
type Repository interface {
	SyncProviders(ctx context.Context, rows []SyncRow) error
	Provider(ctx context.Context, slug string) (ProviderRow, error)
	// IdentityUser ritorna l'utente collegato a (provider, subject).
	IdentityUser(ctx context.Context, providerID uuid.UUID, subject string) (users.User, bool, error)
	// TouchIdentity aggiorna last_login_at ed email dell'identità.
	TouchIdentity(ctx context.Context, providerID uuid.UUID, subject, email string) error
	UserByEmail(ctx context.Context, email string) (users.User, bool, error)
	LinkIdentity(ctx context.Context, userID, providerID uuid.UUID, subject, email string) error
	CreateUser(ctx context.Context, in users.CreateInput) (users.User, error)
	DeleteUser(ctx context.Context, username string) error
	CreateSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (string, sessions.Session, error)
}

// PGStore è il Repository su Postgres.
type PGStore struct {
	pool     *pgxpool.Pool
	users    *users.Service
	sessions *sessions.Store
	now      func() time.Time
}

// NewPGStore crea lo store. now nil usa time.Now.
func NewPGStore(pool *pgxpool.Pool, us *users.Service, ss *sessions.Store, now func() time.Time) *PGStore {
	if now == nil {
		now = time.Now
	}
	return &PGStore{pool: pool, users: us, sessions: ss, now: now}
}

var _ Repository = (*PGStore)(nil)

// SyncProviders fa l'upsert per slug dei provider dati (enabled=true) e
// disabilita quelli non più presenti, in una transazione.
func (s *PGStore) SyncProviders(ctx context.Context, rows []SyncRow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := s.now()
	slugs := make([]string, 0, len(rows))
	for _, r := range rows {
		slugs = append(slugs, r.Slug)
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.oidc_providers
				(id, slug, display_name, issuer_url, client_id, client_secret_enc, enc_key_id, scopes, enabled, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9, $9)
			ON CONFLICT (slug) DO UPDATE SET
				display_name = EXCLUDED.display_name, issuer_url = EXCLUDED.issuer_url,
				client_id = EXCLUDED.client_id, client_secret_enc = EXCLUDED.client_secret_enc,
				enc_key_id = EXCLUDED.enc_key_id, scopes = EXCLUDED.scopes,
				enabled = true, updated_at = EXCLUDED.updated_at`,
			uuid.New(), r.Slug, r.DisplayName, r.Issuer, r.ClientID, r.SecretEnc, r.EncKeyID, r.Scopes, now); err != nil {
			return fmt.Errorf("sincronizzazione del provider %q non riuscita: %w", r.Slug, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE identity.oidc_providers SET enabled = false, updated_at = $2
		WHERE enabled AND NOT (slug = ANY($1::text[]))`, slugs, now); err != nil {
		return fmt.Errorf("disabilitazione dei provider tolti dal file non riuscita: %w", err)
	}
	return tx.Commit(ctx)
}

// Provider legge un provider abilitato per slug.
func (s *PGStore) Provider(ctx context.Context, slug string) (ProviderRow, error) {
	var r ProviderRow
	err := s.pool.QueryRow(ctx, `
		SELECT id, slug, issuer_url, client_id, client_secret_enc, enc_key_id, scopes
		FROM identity.oidc_providers WHERE slug = $1 AND enabled`, slug).
		Scan(&r.ID, &r.Slug, &r.Issuer, &r.ClientID, &r.SecretEnc, &r.EncKeyID, &r.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderRow{}, ErrProviderNotFound
	}
	if err != nil {
		return ProviderRow{}, fmt.Errorf("lettura del provider OIDC non riuscita: %w", err)
	}
	return r, nil
}

func (s *PGStore) IdentityUser(ctx context.Context, providerID uuid.UUID, subject string) (users.User, bool, error) {
	var uid uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT user_id FROM identity.oidc_identities WHERE provider_id = $1 AND subject = $2`,
		providerID, subject).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.User{}, false, nil
	}
	if err != nil {
		return users.User{}, false, fmt.Errorf("lettura dell'identità OIDC non riuscita: %w", err)
	}
	u, err := s.users.GetByID(ctx, uid)
	if err != nil {
		return users.User{}, false, err
	}
	return u, true, nil
}

func (s *PGStore) TouchIdentity(ctx context.Context, providerID uuid.UUID, subject, email string) error {
	var e *string
	if email != "" {
		e = &email
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.oidc_identities SET last_login_at = $3, email = $4
		WHERE provider_id = $1 AND subject = $2`, providerID, subject, s.now(), e)
	return err
}

func (s *PGStore) UserByEmail(ctx context.Context, email string) (users.User, bool, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, users.ErrNotFound) {
		return users.User{}, false, nil
	}
	if err != nil {
		return users.User{}, false, err
	}
	return u, true, nil
}

func (s *PGStore) LinkIdentity(ctx context.Context, userID, providerID uuid.UUID, subject, email string) error {
	var e *string
	if email != "" {
		e = &email
	}
	now := s.now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.oidc_identities (id, user_id, provider_id, subject, email, created_at, last_login_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`, uuid.New(), userID, providerID, subject, e, now)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrIdentityExists
	}
	if err != nil {
		return fmt.Errorf("collegamento dell'identità OIDC non riuscito: %w", err)
	}
	return nil
}

func (s *PGStore) CreateUser(ctx context.Context, in users.CreateInput) (users.User, error) {
	return s.users.Create(ctx, in)
}

func (s *PGStore) DeleteUser(ctx context.Context, username string) error {
	return s.users.Delete(ctx, username)
}

func (s *PGStore) CreateSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (string, sessions.Session, error) {
	return s.sessions.Create(ctx, userID, "oidc", userAgent, ip)
}
