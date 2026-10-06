package users

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// MentionUser è un utente attivo trovato da una menzione.
type MentionUser struct {
	ID       uuid.UUID
	Username string
	Kind     string
}

// MentionTeam è un team trovato da una menzione `org/team`, coi suoi membri
// attivi.
type MentionTeam struct {
	Members []MentionUser
}

// MentionsResult: per ogni nome chiesto (così come arrivato) l'utente o il
// team. I nomi che non corrispondono a niente mancano.
type MentionsResult struct {
	Users map[string]MentionUser
	Teams map[string]MentionTeam
}

// ResolveMentions risolve i nomi `utente` (persona o agente attivo) e
// `org/team` (team esistente, membri attivi), senza distinguere maiuscole
// (I8). Chi vede il repo lo decide il chiamante: qui solo l'esistenza.
func (s *Service) ResolveMentions(ctx context.Context, names []string) (MentionsResult, error) {
	res := MentionsResult{Users: map[string]MentionUser{}, Teams: map[string]MentionTeam{}}
	var userKeys []string
	byKey := map[string][]string{} // minuscolo -> nomi chiesti
	var orgs, teams, teamAsked []string
	for _, n := range names {
		key := strings.ToLower(n)
		if org, team, ok := strings.Cut(key, "/"); ok {
			if org == "" || team == "" || strings.Contains(team, "/") {
				continue
			}
			orgs, teams, teamAsked = append(orgs, org), append(teams, team), append(teamAsked, n)
			continue
		}
		userKeys = append(userKeys, key)
		byKey[key] = append(byKey[key], n)
	}
	if len(userKeys) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT id, username, kind FROM identity.users
			WHERE username = ANY($1) AND is_active`, userKeys)
		if err != nil {
			return res, err
		}
		for rows.Next() {
			var u MentionUser
			if err := rows.Scan(&u.ID, &u.Username, &u.Kind); err != nil {
				rows.Close()
				return res, err
			}
			for _, asked := range byKey[u.Username] {
				res.Users[asked] = u
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}
	}
	if len(teams) == 0 {
		return res, nil
	}
	// Un team per riga, anche senza membri (esiste, ma non notifica nessuno).
	rows, err := s.pool.Query(ctx, `
		SELECT q.asked, u.id, u.username, u.kind
		FROM unnest($1::text[], $2::text[], $3::text[]) AS q(org, team, asked)
		JOIN identity.organizations o ON o.name = q.org
		JOIN identity.teams t ON t.org_id = o.id AND t.name = q.team
		LEFT JOIN identity.team_members m ON m.team_id = t.id
		LEFT JOIN identity.users u ON u.id = m.user_id AND u.is_active
		ORDER BY q.asked, u.username`, orgs, teams, teamAsked)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var asked string
		var id *uuid.UUID
		var username, kind *string
		if err := rows.Scan(&asked, &id, &username, &kind); err != nil {
			return res, err
		}
		t := res.Teams[asked]
		if id != nil && username != nil && kind != nil {
			t.Members = append(t.Members, MentionUser{ID: *id, Username: *username, Kind: *kind})
		}
		res.Teams[asked] = t
	}
	return res, rows.Err()
}
