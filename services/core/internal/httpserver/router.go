package httpserver

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter assembla il router HTTP completo di core:
//   - /healthz, /readyz: probe k8s, non versionate. /readyz verifica la
//     connessione a Postgres.
//   - /health, /resources, /resources/{resourceId}: le operazioni del
//     contratto OpenAPI (generate in internal/openapi), servite senza
//     prefisso di versione: è il gateway che espone /v1/* e rimuove il
//     prefisso instradando a core (vedi
//     services/gateway/internal/proxy.stripV1).
func NewRouter(pool *pgxpool.Pool, publisher events.Publisher) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(pool))

	server := &apiServer{
		pool:      pool,
		resources: store.New(pool),
		events:    publisher,
	}

	return openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseRouter: mux,
	})
}
