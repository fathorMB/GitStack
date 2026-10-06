package httpapi

import (
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
)

// ListOrgOwners (GET /internal/orgs/{orgId}/owners, M-06/G): per core, che
// deve sapere chi può gestire i webhook di un'organizzazione (C6) e a chi
// notificare la disattivazione di uno di essi (C7).
func (s *server) ListOrgOwners(w http.ResponseWriter, r *http.Request, orgID openapi_types.UUID) {
	if _, err := s.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, orgs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Organizzazione non trovata.")
			return
		}
		s.internal(w, r, err)
		return
	}
	ids, err := s.orgs.ListOwners(r.Context(), orgID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := openapi.OrgOwnersResult{Owners: make([]openapi_types.UUID, 0, len(ids))}
	for _, id := range ids {
		out.Owners = append(out.Owners, openapi_types.UUID(id))
	}
	writeJSON(w, http.StatusOK, out)
}
