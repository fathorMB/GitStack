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

	toCore := proxy.ToCore(cfg.CoreURL, cfg.CoreTimeout, logger)
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

	return openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseURL:     proxy.PrefixV1,
		BaseRouter:  mux,
		Middlewares: apiMiddlewares,
	})
}
