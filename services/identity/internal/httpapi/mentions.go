package httpapi

import (
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
)

const maxMentionNames = 100

type mentionUserOut struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	Username string    `json:"username"`
}

// ResolveMentions (POST /internal/mentions/resolve, M-06): per il motore
// delle notifiche di core. Risolve `utente` e `org/team` in utenti attivi
// (I8); i nomi sconosciuti mancano dalla risposta. La forma è quella di
// openapi.ResolveMentionsResult.
func (s *server) ResolveMentions(w http.ResponseWriter, r *http.Request) {
	var in openapi.ResolveMentionsInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if len(in.Names) < 1 || len(in.Names) > maxMentionNames {
		writeError(w, http.StatusBadRequest, "bad_request", "names: da 1 a 100 elementi.")
		return
	}
	found, err := s.users.ResolveMentions(r.Context(), in.Names)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	type userOut struct {
		Name string `json:"name"`
		mentionUserOut
	}
	type teamOut struct {
		Name    string           `json:"name"`
		Members []mentionUserOut `json:"members"`
	}
	out := struct {
		Users []userOut `json:"users"`
		Teams []teamOut `json:"teams"`
	}{Users: []userOut{}, Teams: []teamOut{}}
	userNames := make([]string, 0, len(found.Users))
	for n := range found.Users {
		userNames = append(userNames, n)
	}
	sort.Strings(userNames)
	for _, n := range userNames {
		u := found.Users[n]
		out.Users = append(out.Users, userOut{Name: n, mentionUserOut: mentionUserOut{ID: u.ID, Kind: u.Kind, Username: u.Username}})
	}
	teamNames := make([]string, 0, len(found.Teams))
	for n := range found.Teams {
		teamNames = append(teamNames, n)
	}
	sort.Strings(teamNames)
	for _, n := range teamNames {
		t := teamOut{Name: n, Members: []mentionUserOut{}}
		for _, m := range found.Teams[n].Members {
			t.Members = append(t.Members, mentionUserOut{ID: m.ID, Kind: m.Kind, Username: m.Username})
		}
		out.Teams = append(out.Teams, t)
	}
	writeJSON(w, http.StatusOK, out)
}
