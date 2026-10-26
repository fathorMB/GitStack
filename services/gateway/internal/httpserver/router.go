package httpserver

import (
	"log/slog"
	"net/http"

	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/appauth"
	"github.com/fathorMB/GitStack/services/gateway/internal/config"
	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
	"github.com/fathorMB/GitStack/services/gateway/internal/proxy"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
)

// NewRouter assembla il router HTTP completo del gateway:
//   - /healthz, /readyz: probe k8s, sotto la catena di base (request id +
//     log). Non passano per auth/rate limiting: sono infrastrutturali, non
//     fanno parte dell'API pubblica.
//   - /v1/*: le operazioni del contratto OpenAPI (generate in
//     internal/openapi), instradate verso core, e le rotte di identity.
//     Sotto la catena completa: request id, log, rate limiting (token-bucket per
//     credenziale e per IP) e Auth, che applica a ogni rotta la sua dichiarazione di
//     sicurezza ricavata da api/openapi.yaml (internal/security). Una rotta
//     senza dichiarazione non passa (404).
//
// Se la tabella di sicurezza è incoerente NewRouter va in panic all'avvio: è
// un difetto del codice generato, non della configurazione.
func NewRouter(cfg config.Config, logger *slog.Logger, opts ...Option) http.Handler {
	var o routerOptions
	for _, opt := range opts {
		opt(&o)
	}
	table, err := security.NewTable(security.Routes)
	if err != nil {
		panic(err)
	}

	// Verifier: identity via /internal/verify con cache (README di identity).
	// Senza IdentityURL le rotte autenticate rispondono 503, mai fail open.
	verifier := o.verifier
	var forgetter interface{ Forget(string) }
	if verifier == nil && cfg.IdentityURL != nil {
		client := identityclient.NewClient(cfg.IdentityURL, cfg.IdentityServiceSecret, cfg.IdentityTimeout)
		cache := identityclient.NewCache(client, cfg.AuthCacheTTL, cfg.AuthCacheNegativeTTL, o.now)
		verifier, forgetter = cache, cache
		// Il permesso su risorsa non si tiene in cache: un grant revocato vale
		// subito.
		if o.permissions == nil {
			o.permissions = client
		}
		if o.profiles == nil {
			o.profiles = client
		}
	}

	mux := http.NewServeMux()

	base := middleware.Chain(middleware.RequestID, middleware.Logging(logger, middleware.WithClientIP(func(r *http.Request) string { return proxy.ClientIP(r, cfg.TrustedProxies) })), middleware.Recover(logger))

	mux.Handle("GET /healthz", base(http.HandlerFunc(healthz)))

	readyClient := &http.Client{Timeout: cfg.CoreTimeout}
	mux.Handle("GET /readyz", base(http.HandlerFunc(readyz(cfg.CoreURL, readyClient, cfg.CoreTimeout))))

	// Identità firmata verso i servizi a valle; le credenziali del client
	// (Authorization, cookie) non arrivano a core, che si fida solo
	// dell'identità firmata.
	signing := proxy.WithServiceSecret(cfg.IdentityServiceSecret)
	toCore := proxy.ToCore(cfg.CoreURL, cfg.CoreTimeout, logger,
		proxy.WithTrustedProxies(cfg.TrustedProxies), signing, proxy.WithDropCredentials(), proxy.WithClock(o.now),
		proxy.WithBodyLimits(cfg.MaxBodyBytes, cfg.MaxAttachmentBytes))
	server := &apiServer{proxy: toCore, version: cfg.Version}
	if cfg.RegistryURL != nil {
		server.registryKey = proxy.ToRegistrySigningKey(cfg.RegistryURL, logger, proxy.WithTrustedProxies(cfg.TrustedProxies), proxy.WithClock(o.now))
	}

	mountAppAuth(mux, cfg, logger, base, verifier, o)

	// Middlewares è applicato per ogni operazione generata, dal primo
	// (interno, più vicino al gestore) all'ultimo (esterno): auth e
	// rate limiting girano prima del gestore ma dopo request id/log, così
	// finiscono comunque nei log anche le richieste che in M-02 verranno
	// rifiutate.
	limiter := middleware.NewRateLimiter(middleware.RateLimitConfig{
		CredentialRate: cfg.RateLimit.CredentialRate, CredentialBurst: cfg.RateLimit.CredentialBurst,
		IPRate: cfg.RateLimit.IPRate, IPBurst: cfg.RateLimit.IPBurst,
		ClientIP: func(r *http.Request) string { return proxy.ClientIP(r, cfg.TrustedProxies) },
		Logger:   logger,
		Now:      o.now,
	})
	apiMiddlewares := []openapi.MiddlewareFunc{
		middleware.SecurityHeaders,
		middleware.Auth(middleware.AuthConfig{
			Table: table, Verifier: verifier, Permissions: o.permissions, Forgetter: forgetter,
			Prefix: proxy.PrefixV1, Logger: logger,
		}),
		limiter.Middleware,
		base,
	}

	if cfg.IdentityURL != nil {
		mountIdentity(mux, proxy.ToIdentity(cfg.IdentityURL, cfg.IdentityTimeout, logger, proxy.WithTrustedProxies(cfg.TrustedProxies), signing, proxy.WithClock(o.now),
			proxy.WithBodyLimits(cfg.MaxBodyBytes, cfg.MaxAttachmentBytes)), apiMiddlewares)
	}

	// x-path-tail: il raw per indirizzo ha il ref (con `/`) e il percorso su più
	// segmenti; il codice generato li vede come uno solo.
	mountTail(mux, toCore, apiMiddlewares)

	// Registry OCI (GIT-286, RG1): /v2/ va al servizio git. Passa per request id, log,
	// recover e rate limiting come il resto; l'autenticazione (token Basic) e i permessi
	// del repo li applica il registry, come per lo smart HTTP di git.
	if cfg.RegistryURL != nil {
		reg := base(limiter.Middleware(proxy.ToRegistry(cfg.RegistryURL, logger, proxy.WithTrustedProxies(cfg.TrustedProxies), proxy.WithClock(o.now))))
		mux.Handle("/v2/", reg)
		mux.Handle("/v2", reg)
	}

	return openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseURL:     proxy.PrefixV1,
		BaseRouter:  mux,
		Middlewares: apiMiddlewares,
	})
}

// identityPatterns sono le rotte di api/openapi.yaml servite da identity
// (tag auth, users, tokens, ssh-keys, organizations, teams, permissions),
// nella sintassi di http.ServeMux, con il prefisso di versione. Il
// contratto le elenca tutte sotto questi percorsi; il test
// TestIdentityRoutes_SecondoIlContratto le confronta con openapi.yaml, così
// una rotta nuova non resta fuori dal gateway senza che la CI lo dica.
//
// /internal/* (tag internal: verifica credenziali, permessi, chiavi SSH fra
// servizi) NON è in elenco e non va mai esposto dal gateway: senza pattern,
// il mux risponde 404.
var identityPatterns = []string{
	"/v1/auth/{rest...}",
	"/v1/users",
	"/v1/users/{rest...}",
	"/v1/user/tokens",
	"/v1/user/tokens/{rest...}",
	"/v1/user/export",
	"/v1/user/sessions",
	"/v1/user/sessions/{rest...}",
	"/v1/user/ssh-keys",
	"/v1/user/ssh-keys/{rest...}",
	"/v1/orgs",
	"/v1/orgs/{rest...}",
	"/v1/resources/{resourceId}/grants",
	"/v1/resources/{resourceId}/grants/{grantId}",
	"/v1/resources/{resourceId}/permissions",
}

// mountIdentity registra su mux le rotte di identity, sotto la stessa catena
// di middleware delle operazioni instradate verso core.
func mountIdentity(mux *http.ServeMux, h http.Handler, mws []openapi.MiddlewareFunc) {
	// Stesso ordine di openapi.HandlerWithOptions: il primo elemento è il più
	// interno, l'ultimo il più esterno.
	for _, mw := range mws {
		h = mw(h)
	}
	for _, p := range identityPatterns {
		mux.Handle(p, h)
	}
}

// mountTail registra, sotto la stessa catena di middleware, la coda
// multi-segmento di getRepositoryRawByPath verso core.
func mountTail(mux *http.ServeMux, h http.Handler, mws []openapi.MiddlewareFunc) {
	for _, mw := range mws {
		h = mw(h)
	}
	mux.Handle("GET "+proxy.PrefixV1+"/repos/{owner}/{repo}/raw/{refAndPath...}", h)
}

// Option personalizza NewRouter (usata dai test).
type Option func(*routerOptions)

type routerOptions struct {
	verifier    identityclient.Verifier
	permissions identityclient.PermissionChecker
	profiles    identityclient.ProfileLookup
	appAccess   appauth.AccessPolicy
	now         func() time.Time
}

// WithProfileLookup sostituisce la lettura del profilo utente via identity (test).
func WithProfileLookup(p identityclient.ProfileLookup) Option {
	return func(o *routerOptions) { o.profiles = p }
}

// WithAppAccess sostituisce la regola di accesso alle app (test; la lista di accesso dei tester,
// GIT-376, si innesta qui).
func WithAppAccess(a appauth.AccessPolicy) Option {
	return func(o *routerOptions) { o.appAccess = a }
}

// WithVerifier sostituisce la verifica via identity (test).
func WithVerifier(v identityclient.Verifier) Option {
	return func(o *routerOptions) { o.verifier = v }
}

// WithPermissionChecker sostituisce la verifica dei permessi via identity
// (test).
func WithPermissionChecker(p identityclient.PermissionChecker) Option {
	return func(o *routerOptions) { o.permissions = p }
}

// WithClock sostituisce l'orologio di cache e firma (test).
func WithClock(now func() time.Time) Option {
	return func(o *routerOptions) { o.now = now }
}
