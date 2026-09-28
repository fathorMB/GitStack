// Package httpserver assembla il router HTTP di core: /healthz e /readyz
// (probe k8s, non versionati, con /readyz che verifica la connessione al
// database), l'operazione GetHealth del contratto OpenAPI (GET /health,
// stesso controllo di /readyz, riusato dal gateway per la propria readiness
// verso core) e le operazioni della risorsa di prova (GET/POST /resources,
// GET/PATCH/DELETE /resources/{resourceId}), tutte sullo schema Postgres
// dedicato "core" (D6). Stessa convenzione del gateway
// (services/gateway/internal/httpserver).
package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/db"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CoreVersion è la versione di core riportata da /healthz, /readyz e
// GET /health, allineata a info.version del contratto (api/openapi.yaml).
const CoreVersion = "0.1.0"

const dbPingTimeout = 3 * time.Second

// healthz è la liveness probe: core risponde se il processo è in grado di
// servire richieste HTTP, senza controllare il database (coerente con la
// convenzione del gateway: la liveness non dipende da servizi esterni, così
// un database temporaneamente non raggiungibile non fa riavviare
// continuamente il pod).
func healthz(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, http.StatusOK, openapi.Ok)
}

// readyz è la readiness probe: core è pronto a servire traffico solo se
// riesce a raggiungere Postgres in tempo utile.
func readyz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pingDB(r.Context(), pool) != nil {
			writeHealth(w, http.StatusServiceUnavailable, openapi.Degraded)
			return
		}
		writeHealth(w, http.StatusOK, openapi.Ok)
	}
}

// GetHealth implementa l'operazione GET /health del contratto: stesso
// controllo di /readyz (verifica il database), esposta come operazione
// pubblica perché il gateway la chiama per la propria readiness verso core
// (vedi services/gateway/internal/httpserver.readyz).
func (s *apiServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	if pingDB(r.Context(), s.pool) != nil {
		writeHealth(w, http.StatusServiceUnavailable, openapi.Degraded)
		return
	}
	writeHealth(w, http.StatusOK, openapi.Ok)
}

func pingDB(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, dbPingTimeout)
	defer cancel()
	return db.Ping(ctx, pool)
}

func writeHealth(w http.ResponseWriter, status int, healthStatus openapi.HealthStatus) {
	writeJSON(w, status, openapi.Health{
		Status:  healthStatus,
		Version: CoreVersion,
	})
}
