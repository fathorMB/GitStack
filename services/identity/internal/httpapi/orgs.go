package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// WithOrgs abilita le organizzazioni e i team (tag organizations/teams).
func WithOrgs(o *orgs.Service) Option { return func(s *server) { s.orgs = o } }

// UUIDp ritorna un puntatore a uu.
func UUIDp(uu openapi_types.UUID) *openapi_types.UUID { return &uu }

// Timep ritorna un puntatore a t.
func Timep(t time.Time) *time.Time { return &t }

// orgResolve trasforma un org name string in UUID, cercando l'organizzazione.
// Ritorna l'org e il suo UUID, oppure scrive un errore e ritorna false.
func (s *server) orgResolve(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) (orgs.Organization, openapi_types.UUID, bool) {
	o, err := s.orgs.GetByName(r.Context(), string(org))
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return orgs.Organization{}, openapi_types.UUID{}, false
	}
	if err != nil {
		s.internal(w, r, err)
		return orgs.Organization{}, openapi_types.UUID{}, false
	}
	return o, openapi_types.UUID(o.ID), true
}

// requireOwner risponde 403 se il chiamante non e' admin di sistema ne' owner.
func (s *server) requireOwner(w http.ResponseWriter, r *http.Request, cur auth.Current, orgID uuid.UUID) bool {
	if cur.User.IsAdmin {
		return true
	}
	role, err := s.orgs.Role(r.Context(), orgID, cur.User.ID)
	if err != nil && !errors.Is(err, orgs.ErrNotOrgMember) {
		s.internal(w, r, err)
		return false
	}
	if err != nil || role != "owner" {
		writeError(w, http.StatusForbidden, "forbidden", "Serve il ruolo owner dell'organizzazione.")
		return false
	}
	return true
}

// requireMember risponde 403 se il chiamante non e' admin ne' membro.
func (s *server) requireMember(w http.ResponseWriter, r *http.Request, cur auth.Current, orgID uuid.UUID) bool {
	if cur.User.IsAdmin {
		return true
	}
	if _, err := s.orgs.Role(r.Context(), orgID, cur.User.ID); err != nil {
		if errors.Is(err, orgs.ErrNotOrgMember) {
			writeError(w, http.StatusForbidden, "forbidden", "Non sei membro dell'organizzazione.")
			return false
		}
		s.internal(w, r, err)
		return false
	}
	return true
}

// ---- organizations ----

func (s *server) ListOrganizations(w http.ResponseWriter, r *http.Request, params openapi.ListOrganizationsParams) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	page, perPage := 1, 20
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return
	}
	var orgList []orgs.Organization
	var total int
	var err error
	if cur.User.IsAdmin {
		orgList, total, err = s.orgs.ListAll(r.Context(), page, perPage)
	} else {
		orgList, total, err = s.orgs.List(r.Context(), cur.User.ID, page, perPage)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Organization, 0, len(orgList))
	for _, o := range orgList {
		items = append(items, openapi.Organization{
			Id:          UUIDp(openapi_types.UUID(o.ID)),
			Name:        o.Name,
			Description: &o.Description,
			DisplayName: &o.DisplayName,
			CreatedAt:   Timep(o.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, openapi.OrganizationList{Items: items, Page: page, PerPage: perPage, Total: total})
}
func (s *server) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	var in openapi.CreateOrganizationInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	ci := orgs.CreateOrgInput{Name: string(in.Name), DisplayName: "", Description: ""}
	if in.DisplayName != nil {
		ci.DisplayName = *in.DisplayName
	}
	if in.Description != nil {
		ci.Description = *in.Description
	}
	org, err := s.orgs.Create(r.Context(), ci, cur.User.ID)
	var rne *orgs.ReservedNameError
	if errors.As(err, &rne) {
		writeError(w, http.StatusBadRequest, "reserved_name", "Il nome "+rne.Name+" è riservato.")
		return
	}
	var aee *orgs.AlreadyExistsError
	if errors.As(err, &aee) {
		writeError(w, http.StatusConflict, "already_exists", "Organizzazione gia' esistente.")
		return
	}
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/orgs/"+org.Name)
	writeJSON(w, http.StatusCreated, openapi.Organization{
		Id:          UUIDp(openapi_types.UUID(org.ID)),
		Name:        org.Name,
		Description: &org.Description,
		DisplayName: &org.DisplayName,
		CreatedAt:   Timep(org.CreatedAt),
	})
}

func (s *server) GetOrganization(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireMember(w, r, cur, o.ID) {
		return
	}
	writeJSON(w, http.StatusOK, openapi.Organization{
		Id:          UUIDp(openapi_types.UUID(o.ID)),
		Name:        o.Name,
		Description: &o.Description,
		DisplayName: &o.DisplayName,
		CreatedAt:   Timep(o.CreatedAt),
	})
}

func (s *server) UpdateOrganization(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	var in openapi.UpdateOrganizationInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	ui := orgs.UpdateOrgInput{}
	if in.DisplayName != nil {
		ui.DisplayName = in.DisplayName
	}
	if in.Description != nil {
		ui.Description = in.Description
	}
	result, err := s.orgs.Update(r.Context(), o.ID, ui)
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.Organization{
		Id:          UUIDp(openapi_types.UUID(result.ID)),
		Name:        result.Name,
		Description: &result.Description,
		DisplayName: &result.DisplayName,
		CreatedAt:   Timep(result.CreatedAt),
	})
}

func (s *server) DeleteOrganization(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	err := s.orgs.Delete(r.Context(), o.ID)
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- OrgMembers ----

func (s *server) ListOrgMembers(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, params openapi.ListOrgMembersParams) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireMember(w, r, cur, o.ID) {
		return
	}
	page, perPage := 1, 20
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return
	}
	members, total, err := s.orgs.ListOrgMembers(r.Context(), o.ID, page, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.OrgMember, 0, len(members))
	for _, m := range members {
		user, uerr := s.users.GetByID(r.Context(), m.UserID)
		if uerr != nil {
			s.internal(w, r, uerr)
			return
		}
		items = append(items, openapi.OrgMember{
			User:      toUser(user, true),
			Role:      openapi.OrgRole(m.Role),
			CreatedAt: m.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, openapi.OrgMemberList{Items: items, Page: page, PerPage: perPage, Total: total})
}

func (s *server) SetOrgMember(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, username openapi.UsernameParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	target, err := s.users.Get(r.Context(), string(username))
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var in openapi.SetOrgMemberInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	m, err := s.orgs.SetOrgMember(r.Context(), o.ID, target.ID, orgs.SetOrgMemberInput{Role: string(in.Role)})
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return
	}
	if errors.Is(err, orgs.ErrLastOwner) {
		writeError(w, http.StatusConflict, "last_owner", "Non si puo' rimuovere l'ultimo owner.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	targetUser, terr := s.users.Get(r.Context(), string(username))
	if terr != nil {
		s.internal(w, r, terr)
		return
	}
	writeJSON(w, http.StatusOK, openapi.OrgMember{
		User:      toUser(targetUser, true),
		Role:      openapi.OrgRole(m.Role),
		CreatedAt: m.CreatedAt,
	})
}

func (s *server) RemoveOrgMember(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, username openapi.UsernameParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	target, err := s.users.Get(r.Context(), string(username))
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// Autorizzazione: owner o admin; oppure l'utente rimuove se stesso.
	if !cur.User.IsAdmin && cur.User.ID != target.ID {
		role, err := s.orgs.Role(r.Context(), o.ID, cur.User.ID)
		if errors.Is(err, orgs.ErrNotOrgMember) {
			writeError(w, http.StatusForbidden, "forbidden", "Permesso negato.")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if role != "owner" {
			writeError(w, http.StatusForbidden, "forbidden", "Permesso negato.")
			return
		}
	}
	// Verifica: non declassare o rimuovere l'ultimo owner.
	if err := s.orgs.RemoveOrgMember(r.Context(), o.ID, target.ID); err != nil {
		var ve *orgs.ValidationError
		if errors.As(err, &ve) {
			validationFailed(w, ve.Fields)
			return
		}
		if errors.Is(err, orgs.ErrLastOwner) {
			writeError(w, http.StatusConflict, "last_owner", "Non si puo' rimuovere l'ultimo owner.")
			return
		}
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Teams ----

func (s *server) ListTeams(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, params openapi.ListTeamsParams) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireMember(w, r, cur, o.ID) {
		return
	}
	page, perPage := 1, 20
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return
	}
	teams, total, err := s.orgs.ListTeams(r.Context(), o.ID, page, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Team, 0, len(teams))
	for _, t := range teams {
		items = append(items, openapi.Team{
			Id:          UUIDp(openapi_types.UUID(t.ID)),
			OrgId:       openapi_types.UUID(t.OrgID),
			Name:        t.Name,
			Description: &t.Description,
			CreatedAt:   Timep(t.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, openapi.TeamList{Items: items, Page: page, PerPage: perPage, Total: total})
}

func (s *server) CreateTeam(w http.ResponseWriter, r *http.Request, org openapi.OrgParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	var in openapi.CreateTeamInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	ci := orgs.CreateTeamInput{Name: string(in.Name), Description: ""}
	if in.Description != nil {
		ci.Description = *in.Description
	}
	t, err := s.orgs.CreateTeam(r.Context(), o.ID, ci)
	var aee *orgs.AlreadyExistsError
	if errors.As(err, &aee) {
		writeError(w, http.StatusConflict, "already_exists", "Team gia' esistente.")
		return
	}
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/orgs/"+string(org)+"/teams/"+string(in.Name))
	writeJSON(w, http.StatusCreated, openapi.Team{
		Id:          UUIDp(openapi_types.UUID(t.ID)),
		OrgId:       openapi_types.UUID(t.OrgID),
		Name:        t.Name,
		Description: &t.Description,
		CreatedAt:   Timep(t.CreatedAt),
	})
}

func (s *server) GetTeam(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireMember(w, r, cur, o.ID) {
		return
	}
	t, err := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.Team{
		Id:          UUIDp(openapi_types.UUID(t.ID)),
		OrgId:       openapi_types.UUID(t.OrgID),
		Name:        t.Name,
		Description: &t.Description,
		CreatedAt:   Timep(t.CreatedAt),
	})
}

func (s *server) UpdateTeam(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	// Verifica che il team esista prima di aggiornare.
	existing, err := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var in openapi.UpdateTeamInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	ui := orgs.UpdateTeamInput{}
	if in.Name != nil {
		name := string(*in.Name)
		ui.Name = &name
	}
	if in.Description != nil {
		desc := *in.Description
		ui.Description = &desc
	}
	result, err := s.orgs.UpdateTeam(r.Context(), o.ID, existing.ID, ui)
	var aee *orgs.AlreadyExistsError
	if errors.As(err, &aee) {
		writeError(w, http.StatusConflict, "already_exists", "Team gia' esistente.")
		return
	}
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.Team{
		Id:          UUIDp(openapi_types.UUID(result.ID)),
		OrgId:       openapi_types.UUID(result.OrgID),
		Name:        result.Name,
		Description: &result.Description,
		CreatedAt:   Timep(result.CreatedAt),
	})
}

func (s *server) DeleteTeam(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	t, err := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	err = s.orgs.DeleteTeam(r.Context(), o.ID, t.ID)
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- TeamMembers ----

func (s *server) ListTeamMembers(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam, params openapi.ListTeamMembersParams) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireMember(w, r, cur, o.ID) {
		return
	}
	t, err := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	page, perPage := 1, 20
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return
	}
	members, total, err := s.orgs.ListTeamMembers(r.Context(), o.ID, t.ID, page, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.TeamMember, 0, len(members))
	for _, m := range members {
		user, uerr := s.users.GetByID(r.Context(), m.UserID)
		if uerr != nil {
			s.internal(w, r, uerr)
			return
		}
		items = append(items, openapi.TeamMember{
			User:      toUser(user, true),
			Role:      openapi.TeamRole(m.Role),
			CreatedAt: m.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, openapi.TeamMemberList{Items: items, Page: page, PerPage: perPage, Total: total})
}

func (s *server) SetTeamMember(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam, username openapi.UsernameParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	target, err := s.users.Get(r.Context(), string(username))
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	t, terr := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(terr, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if terr != nil {
		s.internal(w, r, terr)
		return
	}
	var in openapi.SetTeamMemberInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	role := "member"
	if in.Role != nil {
		role = string(*in.Role)
	}
	m, err := s.orgs.SetTeamMember(r.Context(), o.ID, t.ID, target.ID, orgs.SetTeamMemberInput{Role: role})
	var ve *orgs.ValidationError
	if errors.As(err, &ve) {
		validationFailed(w, ve.Fields)
		return
	}
	if errors.Is(err, orgs.ErrNotOrgMember) {
		writeError(w, http.StatusUnprocessableEntity, "not_org_member", "Utente non membro dell'organizzazione.")
		return
	}
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	targetUser, terr := s.users.Get(r.Context(), string(username))
	if terr != nil {
		s.internal(w, r, terr)
		return
	}
	writeJSON(w, http.StatusOK, openapi.TeamMember{
		User:      toUser(targetUser, true),
		Role:      openapi.TeamRole(m.Role),
		CreatedAt: m.CreatedAt,
	})
}

func (s *server) RemoveTeamMember(w http.ResponseWriter, r *http.Request, org openapi.OrgParam, team openapi.TeamParam, username openapi.UsernameParam) {
	if s.orgs == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	o, _, ok := s.orgResolve(w, r, org)
	if !ok {
		return
	}
	if !s.requireOwner(w, r, cur, o.ID) {
		return
	}
	target, err := s.users.Get(r.Context(), string(username))
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	t, terr := s.orgs.GetTeamByName(r.Context(), o.ID, string(team))
	if errors.Is(terr, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if terr != nil {
		s.internal(w, r, terr)
		return
	}
	err = s.orgs.RemoveTeamMemberByOrg(r.Context(), o.ID, t.ID, target.ID)
	if errors.Is(err, orgs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Team non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
