package httpserver

import (
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
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
//
// Identità: core non autentica nessuno, si fida del gateway. Tutte le rotte
// tranne i probe e /health (pubblica nel contratto) richiedono l'identità
// firmata dal gateway con serviceSecret (package trust): senza, o con una
// firma non valida, rispondono 401. Così chi raggiunge core direttamente
// (senza passare dal gateway) non può spacciarsi per un utente.
func NewRouter(pool *pgxpool.Pool, publisher events.Publisher, serviceSecret string, opts ...Option) http.Handler {
	var o routerOptions
	for _, opt := range opts {
		opt(&o)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(pool))

	server := &apiServer{
		pool:      pool,
		resources: store.New(pool),
		events:    publisher,
	}

	openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseRouter: mux,
	})
	return trust.Require(serviceSecret, publicPath, o.now)(mux)
}

// publicPath sono le rotte raggiungibili senza identità: probe k8s e
// /health, dichiarata `security: []` nel contratto.
func publicPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/healthz", "/readyz", "/health":
		return true
	}
	return false
}

// Option personalizza NewRouter (usata dai test).
type Option func(*routerOptions)

type routerOptions struct {
	now func() time.Time
}

// WithClock sostituisce l'orologio con cui si controlla il timestamp della
// firma.
func WithClock(now func() time.Time) Option {
	return func(o *routerOptions) { o.now = now }
}
