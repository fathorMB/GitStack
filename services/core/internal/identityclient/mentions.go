package identityclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// maxResolveMentions è il massimo di nomi per chiamata (contratto di identity).
const maxResolveMentions = 100

// Mentions è il risultato di ResolveMentions. Le chiavi sono i nomi come sono
// stati chiesti (`utente` oppure `org/team`). Un nome sconosciuto manca; un
// team esistente senza membri attivi compare con una lista vuota.
type Mentions struct {
	Users map[string]CodeUser
	Teams map[string][]CodeUser
}

// MentionResolver trasforma le menzioni del testo in utenti
// (POST /internal/mentions/resolve, I8). Implementato da Client.
type MentionResolver interface {
	ResolveMentions(ctx context.Context, names []string) (Mentions, error)
}

type mentionUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Kind     string `json:"kind"`
}

func (u mentionUser) toCodeUser() (CodeUser, error) {
	id, err := uuid.Parse(u.ID)
	if err != nil || (u.Kind != "human" && u.Kind != "agent") {
		return CodeUser{}, fmt.Errorf("%w: utente non valido", ErrUnavailable)
	}
	return CodeUser{ID: id, Username: u.Username, Kind: u.Kind}, nil
}

// ResolveMentions implementa MentionResolver. I nomi si inviano a gruppi da
// 100; errori e risposte malformate sono ErrUnavailable.
func (c *Client) ResolveMentions(ctx context.Context, names []string) (Mentions, error) {
	out := Mentions{Users: map[string]CodeUser{}, Teams: map[string][]CodeUser{}}
	for start := 0; start < len(names); start += maxResolveMentions {
		end := min(start+maxResolveMentions, len(names))
		var res struct {
			Users []struct {
				Name string `json:"name"`
				mentionUser
			} `json:"users"`
			Teams []struct {
				Name    string        `json:"name"`
				Members []mentionUser `json:"members"`
			} `json:"teams"`
		}
		st, err := c.call(ctx, http.MethodPost, "/internal/mentions/resolve", map[string][]string{"names": names[start:end]}, http.StatusOK, &res)
		if err != nil {
			return Mentions{}, err
		}
		if st != http.StatusOK {
			return Mentions{}, fmt.Errorf("%w: la risoluzione delle menzioni ha risposto %d", ErrUnavailable, st)
		}
		for _, u := range res.Users {
			cu, err := u.toCodeUser()
			if err != nil {
				return Mentions{}, err
			}
			out.Users[u.Name] = cu
		}
		for _, t := range res.Teams {
			members := make([]CodeUser, 0, len(t.Members))
			for _, m := range t.Members {
				cu, err := m.toCodeUser()
				if err != nil {
					return Mentions{}, err
				}
				members = append(members, cu)
			}
			out.Teams[t.Name] = members
		}
	}
	return out, nil
}

var _ MentionResolver = (*Client)(nil)
