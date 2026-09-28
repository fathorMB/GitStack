// Package httpserver assembla il router HTTP del gateway: /healthz e
// /readyz (probe k8s, non versionati), e l'API pubblica /v1/* generata dal
// contratto OpenAPI, instradata verso core.
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// GatewayVersion è la versione del gateway riportata da /healthz, allineata
// a info.version del contratto (api/openapi.yaml).
const GatewayVersion = "0.1.0"

// healthz è la liveness probe: il gateway risponde se il processo è in
// grado di servire richieste HTTP, senza controllare le dipendenze. Il
// corpo riusa il modello Health generato dal contratto OpenAPI (stesso
// schema di GET /v1/health), anche se il percorso /healthz non fa parte del
// contratto pubblico: è una convenzione k8s (liveness/readiness), non
// un'operazione dell'API rivolta ai client.
func healthz(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, http.StatusOK, openapi.Ok)
}

// readyz è la readiness probe: il gateway è pronto a servire traffico solo
// se riesce a raggiungere core in tempo utile. In M-01 core è uno
// scheletro: il controllo verifica solo la raggiungibilità.
func readyz(coreURL *url.URL, client *http.Client, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		target := coreURL.ResolveReference(&url.URL{Path: "/health"})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			writeHealth(w, http.StatusServiceUnavailable, openapi.Degraded)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			writeHealth(w, http.StatusServiceUnavailable, openapi.Degraded)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode >= 500 {
			writeHealth(w, http.StatusServiceUnavailable, openapi.Degraded)
			return
		}

		writeHealth(w, http.StatusOK, openapi.Ok)
	}
}

func writeHealth(w http.ResponseWriter, status int, healthStatus openapi.HealthStatus) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openapi.Health{
		Status:  healthStatus,
		Version: GatewayVersion,
	})
}
