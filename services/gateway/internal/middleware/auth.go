package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// SessionCookie è il nome del cookie di sessione web.
const SessionCookie = "gst_session"

// tokenPrefix è il prefisso dei token personali.
const tokenPrefix = "gst_"

// maxCredentialLen è la lunghezza massima di una credenziale: corrisponde a
// VerifyCredentialInput.credential maxLength 512 in api/openapi.yaml.
const maxCredentialLen = 512

// AuthConfig configura Auth.
type AuthConfig struct {
	// Table è la dichiarazione di sicurezza di ogni rotta (da
	// api/openapi.yaml). Una richiesta senza dichiarazione non passa.
	Table *security.Table
	// Verifier verifica le credenziali (di norma identityclient.Cache). Nil:
	// identity non è configurata, le rotte autenticate rispondono 503.
	Verifier identityclient.Verifier
	// Permissions verifica con identity il permesso su risorsa dichiarato da
	// una rotta (x-required-permission). Nil: le rotte che lo richiedono
	// rispondono 503, mai fail open.
	Permissions identityclient.PermissionChecker
	// Forgetter, se presente, svuota la voce di cache di una credenziale
	// dopo una richiesta che ne cambia lo stato (cambio password).
	Forgetter interface{ Forget(credential string) }
	// Prefix è il prefisso di versione dell'API (es. "/v1"), tolto dal
	// percorso prima di cercare la dichiarazione.
	Prefix string
	Logger *slog.Logger
}

// Auth è il middleware di autenticazione centralizzata.
//
//  1. cerca la dichiarazione di sicurezza della rotta; se manca, 404;
//  2. rotta pubblica (`security: []`): passa senza credenziali;
//  3. credenziale da `Authorization: Bearer gst_...` o cookie `gst_session`;
//     assente, di tipo non ammesso dalla rotta o non attiva: 401
//     `unauthenticated`;
//  4. identity irraggiungibile: 503 `identity_unavailable` (mai fail open);
//  5. sessione con password da cambiare: 403 `password_change_required`,
//     salvo le eccezioni del contratto (x-password-change-exempt);
//  6. token senza gli scope della rotta: 403 `insufficient_scope`; le
//     sessioni non hanno scope e passano;
//  7. rotta con permesso su risorsa ({resourceId}): identity dice se l'utente
//     ha almeno quel ruolo (grant diretto, via team, via owner
//     dell'organizzazione, admin di sistema). Negato: 403 `forbidden` senza
//     dati della risorsa, anche se non esiste (nessun 404 che ne riveli
//     l'esistenza); identity non risponde: 503;
//  8. l'identità va nel contesto: il proxy la firma verso i servizi a valle.
func Auth(cfg AuthConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, cfg.Prefix)
			m, ok := cfg.Table.Lookup(r.Method, path)
			if !ok {
				cfg.logger().Warn("rotta senza dichiarazione di sicurezza",
					"request_id", RequestIDFromContext(r.Context()), "method", r.Method, "path", r.URL.Path)
				writeAuthError(w, http.StatusNotFound, "not_found", "Risorsa non trovata.", nil)
				return
			}
			route := m.Route
			if route.Public {
				next.ServeHTTP(w, r)
				return
			}

			cred, kind := credentialFrom(r)
			if len(cred) > maxCredentialLen {
				// identity risponderebbe 400 e il client lo tradurrebbe in 503:
				// una credenziale troppo lunga è solo non valida.
				writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "Credenziale non valida o scaduta.", nil)
				return
			}
			if cred == "" || !route.Accepts(securityKind(kind)) {
				writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "Autenticazione richiesta.", nil)
				return
			}
			if cfg.Verifier == nil {
				writeAuthError(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non è configurato.", nil)
				return
			}
			res, err := cfg.Verifier.Verify(r.Context(), cred, kind)
			if err != nil {
				cfg.logger().Error("verifica della credenziale non riuscita",
					"request_id", RequestIDFromContext(r.Context()), "err", unavailableReason(err))
				writeAuthError(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.", nil)
				return
			}
			if !res.Active {
				writeAuthError(w, http.StatusUnauthorized, "unauthenticated", "Credenziale non valida o scaduta.", nil)
				return
			}
			p := res.Principal

			// Il logout revoca la sessione: la voce in cache non deve restare valida
			// fino al TTL, neanche se identity risponde 204.
			if route.OperationID == "logout" && cfg.Forgetter != nil {
				defer cfg.Forgetter.Forget(cred)
			}

			if p.MustChangePassword {
				exempt := route.PasswordChangeExempt == security.ExemptAlways ||
					(route.PasswordChangeExempt == security.ExemptSelf && m.Params["username"] == p.Username)
				if !exempt {
					writeAuthError(w, http.StatusForbidden, "password_change_required", "La password iniziale va cambiata prima di continuare.", nil)
					return
				}
				// Una richiesta esente può cambiare lo stato (cambio password):
				// la voce in cache non vale più.
				if cfg.Forgetter != nil {
					defer cfg.Forgetter.Forget(cred)
				}
			}

			if p.IsToken {
				if missing := security.Missing(p.Scopes, route.Scopes); len(missing) > 0 {
					writeAuthError(w, http.StatusForbidden, "insufficient_scope", "Il token non ha gli scope richiesti.",
						map[string]any{"required": route.Scopes})
					return
				}
			}

			if route.Permission != security.PermissionNone {
				if !cfg.checkPermission(w, r, p.UserID, m.Params["resourceId"], route.Permission) {
					return
				}
			}

			scopes := p.Scopes
			if !p.IsToken {
				scopes = nil
			}
			ctx := trust.WithIdentity(r.Context(), trust.Identity{UserID: p.UserID, Username: p.Username, Scopes: scopes})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// checkPermission applica il permesso su risorsa; ritorna false dopo aver
// scritto la risposta.
func (c AuthConfig) checkPermission(w http.ResponseWriter, r *http.Request, userID, resourceID string, perm security.Permission) bool {
	if !uuidRE.MatchString(resourceID) {
		// Un id che non è un UUID non può esistere: nessun permesso da chiedere.
		writeAuthError(w, http.StatusBadRequest, "bad_request", "Identificativo della risorsa non valido.", nil)
		return false
	}
	if c.Permissions == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non è configurato.", nil)
		return false
	}
	allowed, err := c.Permissions.CheckPermission(r.Context(), userID, strings.ToLower(resourceID), string(perm))
	if err != nil {
		c.logger().Error("verifica del permesso non riuscita",
			"request_id", RequestIDFromContext(r.Context()), "err", unavailableReason(err))
		writeAuthError(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.", nil)
		return false
	}
	if !allowed {
		writeAuthError(w, http.StatusForbidden, "forbidden", "Permesso negato.", nil)
		return false
	}
	return true
}

var uuidRE = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

func (c AuthConfig) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// credentialFrom estrae la credenziale: prima Authorization: Bearer gst_...,
// poi il cookie di sessione.
func credentialFrom(r *http.Request) (string, identityclient.Kind) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, value, ok := strings.Cut(h, " ")
		value = strings.TrimSpace(value)
		if ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(value, tokenPrefix) {
			return value, identityclient.KindToken
		}
		// Un header Authorization che non è un token gst_ non è una
		// credenziale per il gateway: si guarda il cookie.
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return c.Value, identityclient.KindSession
	}
	return "", ""
}

func securityKind(k identityclient.Kind) security.CredentialKind {
	if k == identityclient.KindToken {
		return security.CredentialToken
	}
	return security.CredentialSession
}

func unavailableReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "richiesta annullata"
	}
	return err.Error()
}

func writeAuthError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	type errBody struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]errBody{"error": {Code: code, Message: message, Details: details}})
}
