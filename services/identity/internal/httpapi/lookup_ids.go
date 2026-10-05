package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
)

const maxLookupIDs = 100

// LookupUsersByIds (POST /internal/users/lookup-ids, M-05): per core, che
// mostra gli utenti delle issues con nome e tipo. Gli id senza utente non
// compaiono nella risposta.
func (s *server) LookupUsersByIds(w http.ResponseWriter, r *http.Request) {
	var in openapi.LookupIdsInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if len(in.Ids) < 1 || len(in.Ids) > maxLookupIDs {
		writeError(w, http.StatusBadRequest, "bad_request", "ids: da 1 a 100 elementi.")
		return
	}
	ids := make([]uuid.UUID, len(in.Ids))
	for i, id := range in.Ids {
		ids[i] = uuid.UUID(id)
	}
	found, err := s.users.LookupByIDs(r.Context(), ids)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var out openapi.LookupIdsResult
	out.Users = make([]struct {
		Id       openapi_types.UUID               `json:"id"`
		Kind     openapi.LookupIdsResultUsersKind `json:"kind"`
		Username openapi.Name                     `json:"username"`
	}, 0, len(found))
	for _, m := range found {
		out.Users = append(out.Users, struct {
			Id       openapi_types.UUID               `json:"id"`
			Kind     openapi.LookupIdsResultUsersKind `json:"kind"`
			Username openapi.Name                     `json:"username"`
		}{Id: openapi_types.UUID(m.ID), Kind: openapi.LookupIdsResultUsersKind(m.Kind), Username: m.Username})
	}
	writeJSON(w, http.StatusOK, out)
}
