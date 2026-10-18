package httpapi

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ListOwnSessions elenca le sessioni web attive del chiamante (F12, V3.3.4).
func (s *server) ListOwnSessions(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	list, err := s.auth.Sessions.ListForUser(r.Context(), cur.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Session, 0, len(list))
	for _, i := range list {
		ua, ip := i.UserAgent, i.IPAddress
		items = append(items, openapi.Session{
			Id:         openapi_types.UUID(i.ID),
			AuthMethod: openapi.SessionAuthMethod(i.AuthMethod),
			UserAgent:  &ua,
			IpAddress:  &ip,
			CreatedAt:  i.CreatedAt,
			LastSeenAt: i.LastSeenAt,
			ExpiresAt:  i.ExpiresAt,
			Current:    i.ID == cur.Session.ID,
		})
	}
	writeJSON(w, http.StatusOK, openapi.SessionList{Items: items})
}

// RevokeOwnSession termina una sessione del chiamante. Quella di un altro
// utente risponde 404, come una inesistente.
func (s *server) RevokeOwnSession(w http.ResponseWriter, r *http.Request, sessionId openapi.SessionIdParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	err := s.auth.Sessions.RevokeByID(r.Context(), cur.User.ID, openapi_types.UUID(sessionId))
	if errors.Is(err, sessions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Sessione non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.secInfo(r, "security.session.revoked", &cur, "revoked_session_id", sessionId.String())
	w.WriteHeader(http.StatusNoContent)
}
