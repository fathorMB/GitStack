package httpapi

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/oidc"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
)

// WithOIDC abilita il login OIDC (listOidcProviders, startOidcLogin,
// finishOidcLogin). Senza (o con un servizio senza provider) l'elenco è vuoto
// e start/callback rispondono 404.
func WithOIDC(o *oidc.Service) Option { return func(s *server) { s.oidc = o } }

func (s *server) ListOidcProviders(w http.ResponseWriter, r *http.Request) {
	items := []openapi.OidcProvider{}
	if s.oidc != nil {
		for _, p := range s.oidc.List() {
			items = append(items, openapi.OidcProvider{Slug: p.Slug, DisplayName: p.DisplayName})
		}
	}
	writeJSON(w, http.StatusOK, openapi.OidcProviderList{Items: items})
}

func providerNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "Provider non trovato.")
}

func (s *server) StartOidcLogin(w http.ResponseWriter, r *http.Request, provider openapi.OidcProviderParam, p openapi.StartOidcLoginParams) {
	if s.oidc == nil {
		providerNotFound(w)
		return
	}
	redirectTo := ""
	if p.RedirectTo != nil {
		redirectTo = *p.RedirectTo
		if !oidc.ValidRedirect(redirectTo) {
			writeError(w, http.StatusBadRequest, "bad_request", "redirectTo deve essere un percorso relativo.")
			return
		}
	}
	res, err := s.oidc.Start(r.Context(), provider, redirectTo)
	switch {
	case errors.Is(err, oidc.ErrDisabled), errors.Is(err, oidc.ErrProviderNotFound):
		providerNotFound(w)
		return
	case errors.Is(err, oidc.ErrInvalidRedirect):
		writeError(w, http.StatusBadRequest, "bad_request", "redirectTo deve essere un percorso relativo.")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, res.Cookie)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, res.URL, http.StatusFound)
}

func (s *server) FinishOidcLogin(w http.ResponseWriter, r *http.Request, provider openapi.OidcProviderParam, p openapi.FinishOidcLoginParams) {
	if s.oidc == nil {
		providerNotFound(w)
		return
	}
	var stateCookie string
	if c, err := r.Cookie(oidc.StateCookieName); err == nil {
		stateCookie = c.Value
	}
	res, err := s.oidc.Finish(r.Context(), oidc.FinishInput{
		Slug: provider, Code: p.Code, State: p.State, StateCookie: stateCookie,
		UserAgent: r.UserAgent(), IP: s.clientIP(r),
	})
	// Il cookie di stato si cancella comunque: non serve più, riuscito o no.
	if err == nil || !errors.Is(err, oidc.ErrDisabled) {
		http.SetCookie(w, s.oidc.ClearStateCookie(provider))
	}
	switch {
	case errors.Is(err, oidc.ErrDisabled), errors.Is(err, oidc.ErrProviderNotFound):
		providerNotFound(w)
		return
	case errors.Is(err, oidc.ErrInvalidState), errors.Is(err, oidc.ErrStateExpired):
		writeError(w, http.StatusBadRequest, "oidc_invalid_state", "Login OIDC scaduto o non valido: riprova dall'inizio.")
		return
	case errors.Is(err, oidc.ErrLoginFailed), errors.Is(err, oidc.ErrUserInactive):
		writeError(w, http.StatusUnauthorized, "oidc_login_failed", "Login OIDC non riuscito.")
		return
	case errors.Is(err, oidc.ErrUnlinked):
		writeError(w, http.StatusConflict, "oidc_identity_unlinked", "L'identità del provider non è collegata a un utente di GitStack.")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(res.SessionValue, res.Session.ExpiresAt))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, res.RedirectTo, http.StatusFound)
}
