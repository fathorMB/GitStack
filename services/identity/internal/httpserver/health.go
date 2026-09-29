// Package httpserver assembla il router HTTP di identity: /healthz e /readyz
// (probe k8s). /healthz risponde sempre "ok" finché il processo è vivo
// (liveness); /readyz risponde "ok" solo se il database è raggiungibile
// entro un timeout breve (readiness).
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const dbPingTimeout = 3 * time.Second

// Healthz è la liveness probe: identity risponde se il processo è in grado di
// servire richieste HTTP, senza controllare il database (coerente con la
// convenzione del gateway: la liveness non dipende da servizi esterni, così
// un database temporaneamente non raggiungibile non fa riavviare
// continuamente il pod).
func Healthz(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, http.StatusOK, "ok")
}

// Readyz è la readiness probe: identity è pronto a servire traffico solo se
// riesce a raggiungere Postgres in tempo utile. Il pool (un *pgxpool.Pool)
// soddisfa l'interfaccia pinger grazie al metodo Ping(context.Context) error.
func Readyz(pool pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pingDB(r.Context(), pool) != nil {
			writeHealth(w, http.StatusServiceUnavailable, "degraded")
			return
		}
		writeHealth(w, http.StatusOK, "ok")
	}
}

// pinger è un'interfaccia che i test usano per simulare il pool:
// solo Ping è necessario per le verifiche di readiness.
type pinger interface {
	Ping(ctx context.Context) error
}

// pingDB verifica che il database sia raggiungibile entro dbPingTimeout.
func pingDB(ctx context.Context, pool pinger) error {
	ctx, cancel := context.WithTimeout(ctx, dbPingTimeout)
	defer cancel()
	return pool.Ping(ctx)
}

// writeHealth scrive una risposta JSON con lo status code e lo stato.
func writeHealth(w http.ResponseWriter, status int, state string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": state})
}
