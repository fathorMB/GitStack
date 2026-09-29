package httpserver

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter costruisce il router con /healthz e /readyz. Il pool è
// usato dal readiness probe per verificare la connettività a Postgres.
func NewRouter(pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Healthz)
	mux.HandleFunc("GET /readyz", Readyz(pool))
	return mux
}
