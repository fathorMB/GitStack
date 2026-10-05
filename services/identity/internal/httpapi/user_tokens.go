package httpapi

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// agentTarget applica la regola P5 ai token di un altro utente. Ordine dei
// controlli: chiamante non admin → 403; username inesistente → 404; utente
// umano → 409 not_an_agent (anche se è l'admin stesso). Se ritorna false ha
// già risposto.
func (s *server) agentTarget(w http.ResponseWriter, r *http.Request, username string) (users.User, bool) {
	if s.tokens == nil {
		unavailable(w)
		return users.User{}, false
	}
	cur, ok := s.current(w, r)
	if !ok {
		return users.User{}, false
	}
	if !cur.User.IsAdmin {
		forbidden(w)
		return users.User{}, false
	}
	u, err := s.users.Get(r.Context(), username)
	if !s.userError(w, r, err) {
		return users.User{}, false
	}
	if u.Kind != users.KindAgent {
		writeError(w, http.StatusConflict, "not_an_agent",
			"I token di una persona li gestisce solo lei: questa operazione vale per gli utenti agent.")
		return users.User{}, false
	}
	return u, true
}

func (s *server) ListUserTokens(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam, p openapi.ListUserTokensParams) {
	agent, ok := s.agentTarget(w, r, username)
	if !ok {
		return
	}
	pg, perPage, ok := page(w, p.Page, p.PerPage)
	if !ok {
		return
	}
	list, total, err := s.tokens.List(r.Context(), agent.ID, pg, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Token, 0, len(list))
	for _, t := range list {
		items = append(items, toToken(t))
	}
	writeJSON(w, http.StatusOK, openapi.TokenList{Items: items, Page: pg, PerPage: perPage, Total: total})
}

func (s *server) CreateUserToken(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	agent, ok := s.agentTarget(w, r, username)
	if !ok {
		return
	}
	var in openapi.CreateTokenInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		scopes = append(scopes, string(sc))
	}
	plain, t, err := s.tokens.Create(r.Context(), apitokens.CreateInput{
		UserID: agent.ID, Name: in.Name, Scopes: scopes, ExpiresAt: in.ExpiresAt,
	})
	var ve *apitokens.ValidationError
	var ne *apitokens.NameInUseError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
		return
	case errors.As(err, &ne):
		writeErrorDetails(w, http.StatusConflict, "already_exists", "name già in uso.", map[string]any{"field": "name"})
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	base := toToken(t)
	w.Header().Set("Location", "/v1/users/"+agent.Username+"/tokens/"+t.ID.String())
	writeJSON(w, http.StatusCreated, openapi.CreatedToken{
		Id: base.Id, Name: base.Name, Scopes: base.Scopes, Hint: base.Hint,
		CreatedAt: base.CreatedAt, ExpiresAt: base.ExpiresAt, LastUsedAt: base.LastUsedAt,
		Token: plain,
	})
}

func (s *server) RevokeUserToken(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam, tokenId openapi.TokenIdParam) {
	agent, ok := s.agentTarget(w, r, username)
	if !ok {
		return
	}
	// Revoke filtra per user_id: un token di un altro utente è ErrNotFound.
	err := s.tokens.Revoke(r.Context(), agent.ID, openapi_types.UUID(tokenId))
	if errors.Is(err, apitokens.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Token non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
