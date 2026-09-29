package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BootstrapAdminInput sono i dati dell'admin iniziale. Password è quella
// generata dall'installer (Secret Kubernetes): non va mai loggata.
type BootstrapAdminInput struct {
	Username string
	Password string
}

// BootstrapAdmin crea l'utente admin iniziale, con must_change=true, solo se
// in identity.users non esiste nessun utente con is_admin. Ritorna created
// true se l'ha creato, false se esisteva già un admin (e allora non tocca
// niente, neanche la password).
//
// Tutto avviene in una transazione che prende adminLockKey
// (pg_advisory_xact_lock): due repliche che partono insieme si serializzano e
// la seconda vede l'admin creato dalla prima. Non passa da SetPassword /
// ChangePassword, che rimettono must_change=false.
func (s *Service) BootstrapAdmin(ctx context.Context, in BootstrapAdminInput) (created bool, err error) {
	if !usernameRe.MatchString(in.Username) {
		return false, &ValidationError{Fields: map[string]string{"username": "non valido"}}
	}
	if perr := password.ValidatePolicy(in.Password, in.Username, ""); perr != nil {
		// perr non contiene la password.
		return false, &ValidationError{Fields: map[string]string{"password": perr.Error()}}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(adminLockKey)); err != nil {
		return false, fmt.Errorf("lock del bootstrap non riuscito: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity.users WHERE is_admin)`).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	// L'hash si calcola dopo aver visto che serve (argon2id è costoso).
	hash, err := password.Hash(in.Password)
	if err != nil {
		return false, err
	}
	now := s.now()
	id := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity.users (id, username, display_name, kind, is_admin, created_at, updated_at)
		VALUES ($1, $2, $2, 'human', true, $3, $3)`, id, in.Username, now); err != nil {
		return false, mapUnique(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity.credentials (user_id, kind, secret_hash, must_change, created_at, updated_at)
		VALUES ($1, 'password', $2, true, $3, $3)`, id, hash, now); err != nil {
		return false, fmt.Errorf("salvataggio della credenziale non riuscito: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// MustChangePassword dice se la password dell'utente va cambiata prima di
// qualunque altra operazione (credenziale password con must_change=true).
func (s *Service) MustChangePassword(ctx context.Context, userID uuid.UUID) (bool, error) {
	var must bool
	err := s.pool.QueryRow(ctx,
		`SELECT must_change FROM identity.credentials WHERE user_id = $1 AND kind = 'password'`, userID).Scan(&must)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return must, err
}
