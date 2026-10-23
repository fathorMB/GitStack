package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// ToRegistry instrada /v2/ (registry OCI, GIT-286) al servizio che lo ospita.
//
// Differenze volute rispetto a ToService, che serve l'API JSON:
//   - Authorization non si toglie: il registry autentica da sé con il token (Basic,
//     come lo smart HTTP di git) e applica i permessi del repo;
//   - nessun timeout né limite al corpo: un'immagine ha livelli da centinaia di MB e
//     un upload dura quanto la rete. Lo fermano la connessione e il registry;
//   - nessuna identità firmata: a valle non c'è nulla che si fidi di X-Gitstack-*, che
//     anzi si cancella se arriva dal client;
//   - gli errori sono nel formato OCI (`errors: [{code, message}]`), quello che
//     `docker` sa mostrare.
func ToRegistry(registryURL *url.URL, logger *slog.Logger, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
			pr.SetXForwarded()
			if proto := forwardedProto(pr.In, o.trusted); proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
			trust.StripClientHeaders(pr.Out.Header)
			pr.Out.Header.Del("Cookie")
			if ip := ClientIP(pr.In, o.trusted); ip != "" {
				pr.Out.Header.Set(ClientIPHeader, ip)
			}
			pr.Out.URL.Scheme = registryURL.Scheme
			pr.Out.URL.Host = registryURL.Host
			pr.Out.Host = registryURL.Host
		},
		// Le risposte (blob) si inoltrano man mano, senza tenerle in memoria.
		FlushInterval: -1,
		// Come per le altre risposte del gateway: niente sniffing e niente cache. I blob sono
		// immutabili per digest, ma l'autorizzazione no: una cache condivisa non deve servirli
		// a chi non ha più il permesso. Set (non Add) sostituisce ciò che mette il registry.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("richiesta verso il registry non riuscita",
				"request_id", middleware.RequestIDFromContext(r.Context()),
				"path", r.URL.Path,
				"err", err,
			)
			writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "il registry non ha risposto")
		},
	}
	return rp
}

func writeOCIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": message}}})
}
