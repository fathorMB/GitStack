package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
)

// headerRequestID è il request id assegnato dal gateway (stesso nome di
// middleware.HeaderRequestID del gateway): lega l'evento alla riga di
// richiesta del gateway.
const headerRequestID = "X-Request-Id"

// secEvent scrive un evento di sicurezza strutturato (campo event=security.*)
// con IP del client e request id. actor, se non nil, è chi compie l'azione.
//
// Mai password, token, cookie o header Authorization: gli attributi sono solo
// identificatori e nomi. Chi chiama passa id di token (non il valore), il
// fingerprint di una chiave (non la chiave) e lo username (non la password).
func (s *server) secEvent(r *http.Request, level slog.Level, event string, actor *auth.Current, attrs ...any) {
	base := []any{
		"event", event,
		"client_ip", s.clientIP(r),
		"request_id", r.Header.Get(headerRequestID),
		"method", r.Method,
		"path", r.URL.Path,
	}
	if actor != nil {
		base = append(base, "actor_id", actor.User.ID.String(), "actor", actor.User.Username)
		if actor.Session.AuthMethod != "" {
			base = append(base, "auth_method", actor.Session.AuthMethod)
		}
		if actor.TokenID != "" {
			base = append(base, "token_id", actor.TokenID)
		}
	}
	s.log.Log(context.Background(), level, "evento di sicurezza", append(base, attrs...)...)
}

// secInfo registra un evento riuscito (login, revoca, grant...).
func (s *server) secInfo(r *http.Request, event string, actor *auth.Current, attrs ...any) {
	s.secEvent(r, slog.LevelInfo, event, actor, attrs...)
}

// secWarn registra un evento di rifiuto o di abuso (login fallito, limite).
func (s *server) secWarn(r *http.Request, event string, actor *auth.Current, attrs ...any) {
	s.secEvent(r, slog.LevelWarn, event, actor, attrs...)
}
