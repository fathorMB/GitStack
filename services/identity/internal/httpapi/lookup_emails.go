package httpapi

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
)

const maxLookupEmails = 100

// LookupUsersByEmail (POST /internal/users/lookup-emails, M-04): per core,
// che collega gli autori dei commit agli utenti. Le email senza utente non
// compaiono nella risposta.
func (s *server) LookupUsersByEmail(w http.ResponseWriter, r *http.Request) {
	var in openapi.LookupEmailsInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if len(in.Emails) < 1 || len(in.Emails) > maxLookupEmails {
		writeError(w, http.StatusBadRequest, "bad_request", "emails: da 1 a 100 elementi.")
		return
	}
	for _, e := range in.Emails {
		if len(e) > 320 {
			writeError(w, http.StatusBadRequest, "bad_request", "emails: al massimo 320 caratteri per email.")
			return
		}
	}
	found, err := s.users.LookupByEmails(r.Context(), in.Emails)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var out openapi.LookupEmailsResult
	out.Users = make([]struct {
		AvatarUrl *string                             `json:"avatarUrl"`
		Email     string                              `json:"email"`
		Id        openapi_types.UUID                  `json:"id"`
		Kind      openapi.LookupEmailsResultUsersKind `json:"kind"`
		Username  openapi.Name                        `json:"username"`
	}, 0, len(found))
	for _, m := range found {
		out.Users = append(out.Users, struct {
			AvatarUrl *string                             `json:"avatarUrl"`
			Email     string                              `json:"email"`
			Id        openapi_types.UUID                  `json:"id"`
			Kind      openapi.LookupEmailsResultUsersKind `json:"kind"`
			Username  openapi.Name                        `json:"username"`
		}{AvatarUrl: m.AvatarURL, Email: m.Email, Id: openapi_types.UUID(m.ID), Kind: openapi.LookupEmailsResultUsersKind(m.Kind), Username: m.Username})
	}
	writeJSON(w, http.StatusOK, out)
}
