package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// LookupUserProfile (POST /internal/users/profile, v3-a GIT-347): per il gateway, che passa a
// un'app interna nome, email e team dell'utente in intestazioni firmate (H6). Solo il servizio.
func (s *server) LookupUserProfile(w http.ResponseWriter, r *http.Request) {
	var in openapi.UserProfileInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	id := uuid.UUID(in.UserId)
	if id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "bad_request", "userId: obbligatorio.")
		return
	}
	u, err := s.users.GetByID(r.Context(), id)
	if errors.Is(err, users.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	teams, err := s.users.TeamsOf(r.Context(), id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := openapi.UserProfileResult{
		Id: openapi_types.UUID(u.ID), Username: u.Username, Kind: openapi.UserProfileResultKind(u.Kind),
		Active: u.IsActive, Teams: teams,
	}
	if u.Email != nil && *u.Email != "" {
		out.Email = u.Email
	}
	writeJSON(w, http.StatusOK, out)
}
