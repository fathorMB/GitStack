package httpapi

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// WithPermissions abilita i grant sulle risorse (tag permissions) e
// /internal/permissions/check.
func WithPermissions(p *permissions.Service) Option { return func(s *server) { s.permissions = p } }

func toGrant(g permissions.Grant) openapi.Grant {
	id, created := openapi_types.UUID(g.ID), g.CreatedAt
	return openapi.Grant{
		Id: &id, ResourceId: openapi_types.UUID(g.ResourceID), SubjectType: openapi.GrantSubjectType(g.SubjectType),
		SubjectId: openapi_types.UUID(g.SubjectID), Role: openapi.ResourceRole(g.Role), CreatedAt: &created,
	}
}

// requireAdmin risolve il chiamante e verifica che sia admin della risorsa
// (o amministratore di sistema). Senza il ruolo risponde 403: non distingue
// fra risorsa inesistente e risorsa altrui.
func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request, resourceID uuid.UUID) (auth.Current, bool) {
	cur, ok := s.current(w, r)
	if !ok {
		return auth.Current{}, false
	}
	if cur.User.IsAdmin {
		return cur, true
	}
	role, err := s.permissions.EffectiveRole(r.Context(), cur.User.ID, resourceID)
	if err != nil {
		s.internal(w, r, err)
		return auth.Current{}, false
	}
	if !role.AtLeast(permissions.RoleAdmin) {
		forbidden(w)
		return auth.Current{}, false
	}
	return cur, true
}

// CheckPermission è POST /internal/permissions/check (protetto da
// serviceAuth). Un utente sconosciuto o disattivato non è un errore: non ha
// nessun permesso.
func (s *server) CheckPermission(w http.ResponseWriter, r *http.Request) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	var in openapi.CheckPermissionInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	role := permissions.Role(in.Role)
	if !role.Valid() {
		validationFailed(w, map[string]string{"role": "read, write o admin"})
		return
	}
	allowed, eff, err := s.permissions.Check(r.Context(), uuid.UUID(in.UserId), uuid.UUID(in.ResourceId), role)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := openapi.CheckPermissionResult{Allowed: allowed}
	if eff != permissions.RoleNone {
		e := openapi.ResourceRole(eff)
		out.EffectiveRole = &e
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) ListResourceGrants(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, p openapi.ListResourceGrantsParams) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	if _, ok := s.requireAdmin(w, r, uuid.UUID(resourceId)); !ok {
		return
	}
	pg, perPage, ok := page(w, p.Page, p.PerPage)
	if !ok {
		return
	}
	list, total, err := s.permissions.List(r.Context(), uuid.UUID(resourceId), pg, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Grant, 0, len(list))
	for _, g := range list {
		items = append(items, toGrant(g))
	}
	writeJSON(w, http.StatusOK, openapi.GrantList{Items: items, Page: pg, PerPage: perPage, Total: total})
}

func (s *server) CreateResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	cur, ok := s.requireAdmin(w, r, uuid.UUID(resourceId))
	if !ok {
		return
	}
	var in openapi.CreateGrantInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	by := cur.User.ID
	g, err := s.permissions.Create(r.Context(), permissions.CreateInput{
		ResourceID: uuid.UUID(resourceId), SubjectType: permissions.SubjectType(in.SubjectType),
		SubjectID: uuid.UUID(in.SubjectId), Role: permissions.Role(in.Role), GrantedBy: &by,
	})
	var ve *permissions.ValidationError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
	case errors.Is(err, permissions.ErrSubjectNotFound):
		validationFailed(w, map[string]string{"subjectId": "utente o team inesistente"})
	case errors.Is(err, permissions.ErrConflict):
		writeError(w, http.StatusConflict, "already_exists", "Il soggetto ha già un grant su questa risorsa.")
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, toGrant(g))
	}
}

func (s *server) UpdateResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, grantId openapi.GrantIdParam) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	if _, ok := s.requireAdmin(w, r, uuid.UUID(resourceId)); !ok {
		return
	}
	var in openapi.UpdateGrantInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	g, err := s.permissions.UpdateRole(r.Context(), uuid.UUID(resourceId), uuid.UUID(grantId), permissions.Role(in.Role))
	var ve *permissions.ValidationError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
	case errors.Is(err, permissions.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Grant non trovato.")
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, toGrant(g))
	}
}

func (s *server) DeleteResourceGrant(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam, grantId openapi.GrantIdParam) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	if _, ok := s.requireAdmin(w, r, uuid.UUID(resourceId)); !ok {
		return
	}
	err := s.permissions.Delete(r.Context(), uuid.UUID(resourceId), uuid.UUID(grantId))
	switch {
	case errors.Is(err, permissions.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Grant non trovato.")
	case err != nil:
		s.internal(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// GetMyResourcePermission: ruolo effettivo del chiamante, `null` se non ha
// accesso (200, non 403: serve proprio a saperlo).
func (s *server) GetMyResourcePermission(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	role, err := s.permissions.EffectiveRole(r.Context(), cur.User.ID, uuid.UUID(resourceId))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := openapi.EffectivePermission{ResourceId: resourceId}
	if role != permissions.RoleNone {
		e := openapi.ResourceRole(role)
		out.Role = &e
	}
	writeJSON(w, http.StatusOK, out)
}

// GrantResourceCreator è POST /internal/resources/{resourceId}/grants/creator
// (protetto da serviceAuth): core lo chiama dopo aver creato una risorsa per
// dare il ruolo admin a chi l'ha creata. Idempotente (200 se il grant
// dell'utente esisteva già, 201 se nuovo).
func (s *server) GrantResourceCreator(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	if s.permissions == nil {
		unavailable(w)
		return
	}
	var in openapi.GrantResourceCreatorInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	g, created, err := s.permissions.GrantCreatorAdmin(r.Context(), uuid.UUID(resourceId), uuid.UUID(in.UserId))
	switch {
	case errors.Is(err, permissions.ErrSubjectNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato o disattivato.")
	case err != nil:
		s.internal(w, r, err)
	case created:
		writeJSON(w, http.StatusCreated, toGrant(g))
	default:
		writeJSON(w, http.StatusOK, toGrant(g))
	}
}
