package identityclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// maxLookupIDs è il massimo di id per chiamata (contratto di identity).
const maxLookupIDs = 100

// UserLookup risolve gli id utente in nome e tipo
// (POST /internal/users/lookup-ids). Implementato da Client. Gli id senza
// utente mancano dalla mappa.
type UserLookup interface {
	LookupUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]CodeUser, error)
}

// LookupUsers implementa UserLookup. Gli id si inviano a gruppi da 100;
// errori e risposte malformate sono ErrUnavailable.
func (c *Client) LookupUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]CodeUser, error) {
	out := make(map[uuid.UUID]CodeUser, len(ids))
	for start := 0; start < len(ids); start += maxLookupIDs {
		end := min(start+maxLookupIDs, len(ids))
		strs := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			strs = append(strs, id.String())
		}
		var res struct {
			Users []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
				Kind     string `json:"kind"`
			} `json:"users"`
		}
		st, err := c.call(ctx, http.MethodPost, "/internal/users/lookup-ids", map[string][]string{"ids": strs}, http.StatusOK, &res)
		if err != nil {
			return nil, err
		}
		if st != http.StatusOK {
			return nil, fmt.Errorf("%w: la ricerca degli utenti per id ha risposto %d", ErrUnavailable, st)
		}
		for _, u := range res.Users {
			id, perr := uuid.Parse(u.ID)
			if perr != nil || (u.Kind != "human" && u.Kind != "agent") {
				return nil, fmt.Errorf("%w: utente non valido", ErrUnavailable)
			}
			out[id] = CodeUser{ID: id, Username: u.Username, Kind: u.Kind}
		}
	}
	return out, nil
}

var _ UserLookup = (*Client)(nil)
