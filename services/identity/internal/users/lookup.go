package users

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// EmailMatch è un utente trovato da un'email di commit.
type EmailMatch struct {
	// Email è l'email così come l'ha richiesta il chiamante.
	Email     string
	ID        uuid.UUID
	Username  string
	Kind      string
	AvatarURL *string
}

// LookupByEmails trova gli utenti con quelle email, senza distinguere le
// maiuscole (indice su lower(email)). Le email senza utente non compaiono.
// Gli utenti disattivati restano: l'autore di un vecchio commit è comunque
// quell'utente.
func (s *Service) LookupByEmails(ctx context.Context, emails []string) ([]EmailMatch, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	lowered := make([]string, len(emails))
	byLower := make(map[string][]string, len(emails))
	for i, e := range emails {
		l := strings.ToLower(e)
		lowered[i] = l
		byLower[l] = append(byLower[l], e)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT lower(email), id, username, kind, avatar_url
		FROM identity.users WHERE lower(email) = ANY($1)`, lowered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmailMatch
	for rows.Next() {
		var (
			l string
			m EmailMatch
		)
		if err := rows.Scan(&l, &m.ID, &m.Username, &m.Kind, &m.AvatarURL); err != nil {
			return nil, err
		}
		for _, orig := range byLower[l] {
			mm := m
			mm.Email = orig
			out = append(out, mm)
		}
	}
	return out, rows.Err()
}
