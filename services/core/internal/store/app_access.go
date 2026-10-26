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

// Lista di accesso dell'app (v3-a, GIT-376, H6 del 2026-10-26). Chi è nella lista apre l'app e
// nient'altro: la riga non è un ruolo sul repo, quindi niente qui tocca i permessi (H5). Un
// membro è un utente (UserID) o un team (TeamName = «organizzazione/team», minuscolo).

// ErrAppAccessMemberExists: l'utente o il team è già nella lista.
var ErrAppAccessMemberExists = errors.New("già nella lista di accesso dell'app")

// AppAccessMember è una riga della lista di accesso.
type AppAccessMember struct {
	ID       uuid.UUID
	AppID    uuid.UUID
	Kind     string // user | team
	UserID   *uuid.UUID
	TeamName *string
	AddedBy  *AppActor
	AddedAt  time.Time
}

const appAccessMemberCols = `id, app_id, kind, user_id, team_name, added_by, created_at`

func scanAppAccessMember(row pgx.Row) (AppAccessMember, error) {
	var m AppAccessMember
	var by []byte
	if err := row.Scan(&m.ID, &m.AppID, &m.Kind, &m.UserID, &m.TeamName, &by, &m.AddedAt); err != nil {
		return AppAccessMember{}, err
	}
	if len(by) > 0 {
		m.AddedBy = &AppActor{}
		if err := json.Unmarshal(by, m.AddedBy); err != nil {
			return AppAccessMember{}, err
		}
	}
	return m, nil
}

// ListAppAccessMembers elenca la lista dell'app, dal più vecchio.
func (s *Store) ListAppAccessMembers(ctx context.Context, appID uuid.UUID) ([]AppAccessMember, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+appAccessMemberCols+` FROM core.app_access_members WHERE app_id = $1 ORDER BY created_at, id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppAccessMember{}
	for rows.Next() {
		m, err := scanAppAccessMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddAppAccessMember aggiunge un utente (userID) o un team (teamName, minuscolo) alla lista.
// ErrAppAccessMemberExists se c'è già.
func (s *Store) AddAppAccessMember(ctx context.Context, appID uuid.UUID, userID *uuid.UUID, teamName *string, by *AppActor, now time.Time) (AppAccessMember, error) {
	kind := "user"
	if teamName != nil {
		kind = "team"
	}
	m, err := scanAppAccessMember(s.pool.QueryRow(ctx, `INSERT INTO core.app_access_members (id, app_id, kind, user_id, team_name, added_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+appAccessMemberCols, uuid.New(), appID, kind, userID, teamName, actorJSON(by), now))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return AppAccessMember{}, ErrAppAccessMemberExists
	}
	return m, err
}

// RemoveAppAccessMember toglie un membro dalla lista dell'app; ErrNotFound se non c'è.
func (s *Store) RemoveAppAccessMember(ctx context.Context, appID, memberID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_access_members WHERE app_id = $1 AND id = $2`, appID, memberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
