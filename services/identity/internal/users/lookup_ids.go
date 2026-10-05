package users

import (
	"context"

	"github.com/google/uuid"
)

// IDMatch è un utente trovato per id.
type IDMatch struct {
	ID       uuid.UUID
	Username string
	Kind     string
}

// LookupByIDs trova gli utenti con quegli id. Gli id senza utente non
// compaiono; gli utenti disattivati restano (l'autore di una issue è
// comunque quell'utente).
func (s *Service) LookupByIDs(ctx context.Context, ids []uuid.UUID) ([]IDMatch, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, username, kind FROM identity.users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IDMatch
	for rows.Next() {
		var m IDMatch
		if err := rows.Scan(&m.ID, &m.Username, &m.Kind); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
