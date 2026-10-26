package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Lista di accesso dell'app (v3-a, GIT-376, H6 del 2026-10-26, pilota retail.it). Contiene utenti e
// team GitStack (nessun accesso anonimo, P2) e la gestisce chi ha `admin` sull'app, cioè sul repo
// (H5). Chi entra solo dalla lista apre l'app e nient'altro: la lista vive qui, in core, e non
// diventa mai un grant di identity, quindi il repo resta invisibile (404 sulle sue API) e H5 non
// cambia. Il forward-auth del gateway la legge con GetAppAccessMembership.

// accessResolvers: identity per risolvere utenti e team; assenti = 503 dove serve (mai fail open).
func (s *apiServer) accessResolvers() (identityclient.MentionResolver, identityclient.UserLookup) {
	res, _ := s.repoIdentity.(identityclient.MentionResolver)
	look, _ := s.repoIdentity.(identityclient.UserLookup)
	return res, look
}

func normalizeTeamName(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	org, team, ok := strings.Cut(n, "/")
	if !ok || org == "" || team == "" || strings.Contains(team, "/") || len(n) > 200 || strings.ContainsAny(n, " \t\r\n") {
		return "", false
	}
	return n, true
}

// memberToAPI traduce una riga; names/emails/counts arrivano da identity (best effort per la lista).
func memberToAPI(m store.AppAccessMember, user *identityclient.CodeUser, teamSize *int) openapi.AppAccessMember {
	out := openapi.AppAccessMember{Id: openapi_types.UUID(m.ID), Kind: openapi.AppAccessMemberKind(m.Kind), AddedAt: m.AddedAt}
	if m.AddedBy != nil {
		out.AddedBy = &openapi.AppActor{Id: openapi_types.UUID(m.AddedBy.ID), Username: m.AddedBy.Username, Type: openapi.AppActorType(m.AddedBy.Type)}
	}
	if m.Kind == "team" && m.TeamName != nil {
		out.Name = *m.TeamName
		out.MemberCount = teamSize
		return out
	}
	if m.UserID != nil {
		out.Name = m.UserID.String()
	}
	if user != nil {
		out.Name = user.Username
		if user.Email != "" {
			e := user.Email
			out.Email = &e
		}
	}
	return out
}

// membersToAPI arricchisce le righe con nome ed email degli utenti e con la dimensione dei team.
func (s *apiServer) membersToAPI(r *http.Request, rows []store.AppAccessMember) []openapi.AppAccessMember {
	res, look := s.accessResolvers()
	var ids []uuid.UUID
	var teams []string
	for _, m := range rows {
		if m.UserID != nil {
			ids = append(ids, *m.UserID)
		}
		if m.TeamName != nil {
			teams = append(teams, *m.TeamName)
		}
	}
	found := map[uuid.UUID]identityclient.CodeUser{}
	if look != nil && len(ids) > 0 {
		if f, err := look.LookupUsers(r.Context(), ids); err == nil {
			found = f
		} else {
			slog.Default().Warn("lista di accesso: lettura degli utenti non riuscita", "err", err)
		}
	}
	sizes := map[string]int{}
	if res != nil && len(teams) > 0 {
		if m, err := res.ResolveMentions(r.Context(), teams); err == nil {
			for name, members := range m.Teams {
				sizes[strings.ToLower(name)] = len(members)
			}
		} else {
			slog.Default().Warn("lista di accesso: lettura dei team non riuscita", "err", err)
		}
	}
	out := make([]openapi.AppAccessMember, 0, len(rows))
	for _, m := range rows {
		var u *identityclient.CodeUser
		if m.UserID != nil {
			if c, ok := found[*m.UserID]; ok {
				u = &c
			}
		}
		var size *int
		if m.TeamName != nil {
			if n, ok := sizes[*m.TeamName]; ok {
				size = &n
			}
		}
		out = append(out, memberToAPI(m, u, size))
	}
	return out
}

// ListAppAccessMembers implementa GET /repos/{owner}/{repo}/apps/{app}/access/members (admin).
func (s *apiServer) ListAppAccessMembers(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	rows, err := s.resources.ListAppAccessMembers(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "lettura della lista di accesso", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.AppAccessMemberList{Items: s.membersToAPI(r, rows)})
}

// AddAppAccessMember implementa POST /repos/{owner}/{repo}/apps/{app}/access/members (admin).
func (s *apiServer) AddAppAccessMember(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppAccessMemberInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIssueRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	res, _ := s.accessResolvers()
	if res == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile risolvere utenti e team.")
		return
	}
	var userID *uuid.UUID
	var team *string
	var asked string
	switch in.Kind {
	case openapi.AppAccessMemberInputKindUser:
		asked = strings.TrimSpace(in.Name)
		if asked == "" || strings.Contains(asked, "/") {
			writeFieldError(w, "name", "name: il nome di un utente, senza «/».")
			return
		}
	case openapi.AppAccessMemberInputKindTeam:
		n, ok := normalizeTeamName(in.Name)
		if !ok {
			writeFieldError(w, "name", "name: un team si scrive organizzazione/team.")
			return
		}
		asked, team = n, &n
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "kind: user o team.")
		return
	}
	found, err := res.ResolveMentions(r.Context(), []string{asked})
	if err != nil {
		slog.Default().Warn("lista di accesso: risoluzione non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile risolvere utenti e team.")
		return
	}
	var size *int
	if team != nil {
		members, ok := found.Teams[asked]
		if !ok {
			writeFieldError(w, "name", fmt.Sprintf("name: il team %s non esiste.", asked))
			return
		}
		n := len(members)
		size = &n
	} else {
		u, ok := found.Users[asked]
		if !ok {
			writeFieldError(w, "name", fmt.Sprintf("name: l'utente %s non esiste o è disattivato.", asked))
			return
		}
		userID = &u.ID
	}
	row, err := s.resources.AddAppAccessMember(r.Context(), a.ID, userID, team, actor.store(), s.now())
	if errors.Is(err, store.ErrAppAccessMemberExists) {
		writeError(w, http.StatusConflict, "already_member", "Già nella lista di accesso dell'app.")
		return
	}
	if err != nil {
		appInternalError(w, "aggiunta alla lista di accesso", err)
		return
	}
	var u *identityclient.CodeUser
	if userID != nil {
		cu := found.Users[asked]
		u = &cu
		if _, look := s.accessResolvers(); look != nil {
			if f, err := look.LookupUsers(r.Context(), []uuid.UUID{*userID}); err == nil {
				if c, ok := f[*userID]; ok {
					u = &c
				}
			}
		}
	}
	writeJSON(w, http.StatusCreated, memberToAPI(row, u, size))
}

// RemoveAppAccessMember implementa DELETE .../access/members/{memberId} (admin).
func (s *apiServer) RemoveAppAccessMember(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, memberID openapi_types.UUID) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	err := s.resources.RemoveAppAccessMember(r.Context(), a.ID, uuid.UUID(memberID))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Non è nella lista di accesso.")
		return
	}
	if err != nil {
		appInternalError(w, "rimozione dalla lista di accesso", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetAppAccessMembership implementa GET /apps/{appId}/access-membership: il chiamante (firmato dal
// gateway per il forward-auth) è nella lista, direttamente o tramite un team? Non dà permessi.
// Se identity non risponde, 503: il gateway non apre mai l'app per un «non so».
func (s *apiServer) GetAppAccessMembership(w http.ResponseWriter, r *http.Request, appID openapi_types.UUID) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	member, err := s.isAccessListMember(r, uuid.UUID(appID), userID)
	if err != nil {
		slog.Default().Warn("lista di accesso: verifica non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Impossibile verificare la lista di accesso.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, openapi.AppAccessMembership{Member: member})
}

func (s *apiServer) isAccessListMember(r *http.Request, appID, userID uuid.UUID) (bool, error) {
	rows, err := s.resources.ListAppAccessMembers(r.Context(), appID)
	if err != nil {
		return false, err
	}
	var teams []string
	for _, m := range rows {
		if m.UserID != nil && *m.UserID == userID {
			return true, nil
		}
		if m.TeamName != nil {
			teams = append(teams, *m.TeamName)
		}
	}
	if len(teams) == 0 {
		return false, nil
	}
	res, _ := s.accessResolvers()
	if res == nil {
		return false, errors.New("identity non configurata")
	}
	found, err := res.ResolveMentions(r.Context(), teams)
	if err != nil {
		return false, err
	}
	for _, members := range found.Teams {
		for _, u := range members {
			if u.ID == userID {
				return true, nil
			}
		}
	}
	return false, nil
}

// GetAppAccessInvite implementa GET .../access/invite (admin): i campi dell'invito e il testo composto.
func (s *apiServer) GetAppAccessInvite(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	addr := s.appAddress(a.AddressLabel)
	if addr == nil {
		writeError(w, http.StatusConflict, "no_address", "L'app non ha un indirizzo: serve una porta e il dominio delle app.")
		return
	}
	// L'impronta è quella di GET /instance/ca (GIT-383), letta dalla stessa funzione: mai ricalcolata.
	ca := s.instanceCa()
	host := *addr
	if u, err := url.Parse(*addr); err == nil && u.Host != "" {
		host = u.Host
	}
	guide := strings.TrimRight(s.clone.PublicURL, "/") + "/help/ca?" + url.Values{"app": {host}}.Encode()
	out := openapi.AppAccessInvite{GuideUrl: guide, Address: *addr, CaMode: openapi.AppAccessInviteCaMode(ca.Mode)}
	if ca.Mode == openapi.InstanceCaModeInternal && ca.FingerprintSha256 != nil {
		out.FingerprintSha256 = ca.FingerprintSha256
	}
	out.Text = inviteText(a.Name, a.Owner+"/"+a.RepoName, guide, out.FingerprintSha256, *addr)
	writeJSON(w, http.StatusOK, out)
}

// inviteText compone l'invito come nel mockup H13 (data-screen="testers", .prisma/design/mockups-v3-hosting).
// Senza CA interna (certificato del cliente o niente HTTPS) non c'è niente da installare: manca il passo della guida.
func inviteText(app, repo, guide string, fingerprint *string, address string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi, you can now test %s (%s).\n\n", app, repo)
	n := 1
	if fingerprint != nil {
		fmt.Fprintf(&b, "%d. Open %s and follow the steps for your device.\n", n, guide)
		b.WriteString("   Your browser may warn you on that page: continue only if the page shows this fingerprint:\n")
		fmt.Fprintf(&b, "   SHA-256 %s\n", *fingerprint)
		n++
	}
	fmt.Fprintf(&b, "%d. Open %s and sign in with your GitStack account.\n", n, address)
	n++
	fmt.Fprintf(&b, "%d. Found a problem? Use \"Report a problem\" inside the app.", n)
	return b.String()
}
