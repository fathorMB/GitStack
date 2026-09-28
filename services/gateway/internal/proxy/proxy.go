// Package proxy instrada le richieste dell'API pubblica verso il servizio
// core, secondo il contratto OpenAPI: il gateway non implementa la logica di
// business, la passa a valle. In M-01 core è ancora uno scheletro, quindi il
// proxy è generico: non conosce le singole operazioni, si limita a
// riscrivere schema/host/percorso e a trasmettere metodo, header, query e
// corpo.
package proxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// PrefixV1 è il prefisso di versione dell'API pubblica esposta dal gateway
// (vedi api/openapi.yaml, servers[0].url). Core espone le stesse operazioni
// senza questo prefisso: il proxy lo rimuove instradando a valle.
const PrefixV1 = "/v1"

// ToCore costruisce l'handler HTTP che instrada le richieste verso core.
// coreURL è la base URL del servizio core (es. http://core:8080); timeout è
// applicato per ciascuna richiesta instradata.
func ToCore(coreURL *url.URL, timeout time.Duration, logger *slog.Logger) http.Handler {
	rp := &httputil.ReverseProxy{
		// Rewrite sostituisce Director (deprecato dal Go 1.26). A differenza
		// di Director, ReverseProxy non propaga più da solo la catena
		// X-Forwarded-For: bisogna conservarla esplicitamente prima di
		// chiamare SetXForwarded, che poi vi accoda l'IP del client
		// (pr.In.RemoteAddr) e imposta anche X-Forwarded-Host/Proto. Senza
		// questo, core perderebbe l'IP del client, necessario per audit e
		// rate limit in M-02 (vedi [c_026a583e9e6d9ab8]).
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
			pr.SetXForwarded()
			pr.Out.URL.Scheme = coreURL.Scheme
			pr.Out.URL.Host = coreURL.Host
			pr.Out.Host = coreURL.Host
			pr.Out.URL.Path = stripV1(pr.In.URL.Path)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("richiesta verso core non riuscita",
				"request_id", middleware.RequestIDFromContext(r.Context()),
				"path", r.URL.Path,
				"err", err,
			)
			writeError(w, http.StatusServiceUnavailable, "core_unavailable", "Il servizio core non ha risposto in tempo.")
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		rp.ServeHTTP(w, r.WithContext(ctx))
	})
}

// stripV1 rimuove il prefisso di versione /v1 dal percorso, per instradare a
// core il percorso "nudo" del contratto (es. /v1/health -> /health).
func stripV1(path string) string {
	trimmed := strings.TrimPrefix(path, PrefixV1)
	if trimmed == "" {
		return "/"
	}
	return trimmed
}

// writeError scrive un errore nel formato unico del contratto (schema
// Error), così anche gli errori generati dal gateway stesso (non da core)
// restano conformi al contratto OpenAPI.
func writeError(w http.ResponseWriter, status int, code, message string) {
	var body openapi.Error
	body.Error.Code = code
	body.Error.Message = message

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
