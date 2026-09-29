package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
	"github.com/fathorMB/GitStack/services/gateway/internal/proxy"
)

// NewRouter assembla il router HTTP completo del gateway:
//   - /healthz, /readyz: probe k8s, sotto la catena di base (request id +
//     log). Non passano per auth/rate limiting: sono infrastrutturali, non
//     fanno parte dell'API pubblica.
//   - /v1/*: le operazioni del contratto OpenAPI (generate in
//     internal/openapi), instradate verso core. Sotto la catena completa:
//     request id, log, poi i punti di aggancio per auth e rate limiting
//     (no-op in M-01, pronti per M-02).
func NewRouter(cfg config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	base := middleware.Chain(middleware.RequestID, middleware.Logging(logger))

	mux.Handle("GET /healthz", base(http.HandlerFunc(healthz)))

	readyClient := &http.Client{Timeout: cfg.CoreTimeout}
	mux.Handle("GET /readyz", base(http.HandlerFunc(readyz(cfg.CoreURL, readyClient, cfg.CoreTimeout))))

	toCore := proxy.ToCore(cfg.CoreURL, cfg.CoreTimeout, logger, proxy.WithTrustedProxies(cfg.TrustedProxies))
	server := &apiServer{proxy: toCore}

	// Middlewares è applicato per ogni operazione generata, dal primo
	// (interno, più vicino al gestore) all'ultimo (esterno): auth e
	// rate limiting girano prima del gestore ma dopo request id/log, così
	// finiscono comunque nei log anche le richieste che in M-02 verranno
	// rifiutate.
	apiMiddlewares := []openapi.MiddlewareFunc{
		middleware.RateLimit,
		middleware.Auth,
		base,
	}

	if cfg.IdentityURL != nil {
		mountIdentity(mux, proxy.ToIdentity(cfg.IdentityURL, cfg.IdentityTimeout, logger, proxy.WithTrustedProxies(cfg.TrustedProxies)), apiMiddlewares)
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
