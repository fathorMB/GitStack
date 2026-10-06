package identityclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// maxLookupEmails è il massimo di email per chiamata (contratto di identity).
const maxLookupEmails = 100

// CodeUser è l'utente GitStack che corrisponde all'email di un commit.
type CodeUser struct {
	ID        uuid.UUID
	Username  string
	Kind      string // human | agent
	AvatarURL *string
	// Email è valorizzata solo da LookupUsers (POST /internal/users/lookup-ids)
	// e solo se l'utente ne ha una; le notifiche email (C5) la usano.
	Email string
}

// EmailLookup collega le email dei commit agli utenti
// (POST /internal/users/lookup-emails). Implementato da Client. Le chiavi
// della mappa sono le email in minuscolo.
type EmailLookup interface {
	LookupEmails(ctx context.Context, emails []string) (map[string]CodeUser, error)
}

// LookupEmails implementa EmailLookup. Le email si inviano a gruppi da 100;
// errori e risposte malformate sono ErrUnavailable.
func (c *Client) LookupEmails(ctx context.Context, emails []string) (map[string]CodeUser, error) {
	out := make(map[string]CodeUser)
	for start := 0; start < len(emails); start += maxLookupEmails {
		end := min(start+maxLookupEmails, len(emails))
		var res struct {
			Users []struct {
				Email     string  `json:"email"`
				ID        string  `json:"id"`
				Username  string  `json:"username"`
				Kind      string  `json:"kind"`
				AvatarURL *string `json:"avatarUrl"`
			} `json:"users"`
		}
		st, err := c.call(ctx, http.MethodPost, "/internal/users/lookup-emails", map[string][]string{"emails": emails[start:end]}, http.StatusOK, &res)
		if err != nil {
			return nil, err
		}
		if st != http.StatusOK {
			return nil, fmt.Errorf("%w: la ricerca degli utenti per email ha risposto %d", ErrUnavailable, st)
		}
		for _, u := range res.Users {
			id, perr := uuid.Parse(u.ID)
			if perr != nil || (u.Kind != "human" && u.Kind != "agent") {
				return nil, fmt.Errorf("%w: utente non valido", ErrUnavailable)
			}
			out[strings.ToLower(u.Email)] = CodeUser{ID: id, Username: u.Username, Kind: u.Kind, AvatarURL: u.AvatarURL}
		}
	}
	return out, nil
}

var _ EmailLookup = (*Client)(nil)
