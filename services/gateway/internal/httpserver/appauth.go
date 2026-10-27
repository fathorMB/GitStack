package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/appauth"
	"github.com/fathorMB/GitStack/services/gateway/internal/config"
	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
)

// mountAppAuth monta il login davanti alle app interne (v3-a, GIT-347, H6): il forward-auth di
// Traefik, il passaggio dal login GitStack e le chiavi pubbliche per verificare le intestazioni.
// Stanno fuori da /v1 e dal contratto OpenAPI: li usano Traefik e il browser, non i client API.
// Se le chiavi di firma mancano o non sono valide il forward-auth risponde 503: nessuna app
// «login» passa senza (mai fail open). Sull'host di GitStack passano dal prefisso /api del chart.
func mountAppAuth(mux *http.ServeMux, cfg config.Config, logger *slog.Logger, base middleware.Middleware,
	verifier identityclient.Verifier, o routerOptions) {
	mountAppAuthRoutes(mux, cfg, logger, base, verifier, o, false)
}

// mountAppKeys monta sulla porta interna (GIT-444, H6/H16) la sola rotta delle chiavi pubbliche: le
// app ospitate la raggiungono (GITSTACK_APP_KEYS_URL) per verificare le intestazioni firmate. Sono
// pubbliche e senza autenticazione; verify e login restano fuori dalla porta interna (GIT-442).
func mountAppKeys(mux *http.ServeMux, cfg config.Config, logger *slog.Logger, base middleware.Middleware) {
	mountAppAuthRoutes(mux, cfg, logger, base, nil, routerOptions{}, true)
}

func mountAppAuthRoutes(mux *http.ServeMux, cfg config.Config, logger *slog.Logger, base middleware.Middleware,
	verifier identityclient.Verifier, o routerOptions, keysOnly bool) {
	ac := appauth.Config{
		Verifier: verifier, Profiles: o.profiles, Permissions: o.permissions, Access: o.appAccess,
		Secret: cfg.IdentityServiceSecret, PublicURL: cfg.PublicURL, AppsDomain: cfg.AppsDomain,
		Now: o.now, Logger: logger,
	}
	if cfg.AppAuthSigningKeys != "" {
		signer, err := appauth.ParseSigningKeys(cfg.AppAuthSigningKeys)
		if err != nil {
			logger.Error("appauth: chiavi di firma non valide, il login davanti alle app resta spento", "err", err.Error())
		} else {
			ac.Signer = signer
		}
	}
	if keysOnly {
		h := appauth.New(ac)
		mux.Handle("GET "+appauth.PathKeys, base(http.HandlerFunc(h.ServeKeys)))
		return
	}
	// Lista di accesso dei tester (GIT-376): read sul repo OPPURE membro della lista, chiesta a core.
	// Un test può sostituire tutta la regola con WithAppAccess.
	if ac.Access == nil && cfg.CoreURL != nil && cfg.IdentityServiceSecret != "" && o.permissions != nil {
		ac.Access = appauth.WithAccessList(appauth.DefaultAccess(o.permissions), appauth.NewCoreMembership(cfg.CoreURL, cfg.IdentityServiceSecret, cfg.CoreTimeout, o.now))
	}
	h := appauth.New(ac)
	mux.Handle(appauth.PathVerify, base(http.HandlerFunc(h.ServeVerify)))
	mux.Handle("GET "+appauth.PathLogin, base(http.HandlerFunc(h.ServeLogin)))
	mux.Handle("GET "+appauth.PathKeys, base(http.HandlerFunc(h.ServeKeys)))
}
