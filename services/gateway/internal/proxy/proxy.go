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
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// PrefixV1 è il prefisso di versione dell'API pubblica esposta dal gateway
// (vedi api/openapi.yaml, servers[0].url). Core espone le stesse operazioni
// senza questo prefisso: il proxy lo rimuove instradando a valle.
const PrefixV1 = "/v1"

// ClientIPHeader è l'header con cui il gateway comunica ai servizi a valle
// l'IP del client. Il gateway lo imposta sempre lui, cancellando qualunque
// valore arrivato dal client: identity lo legge solo da proxy fidati
// (services/identity/internal/httpapi, stesso nome).
const ClientIPHeader = "X-Gitstack-Client-Ip"

const (
	// DefaultMaxBodyBytes è il tetto di default al corpo di una richiesta
	// instradata (JSON): 1 MiB, lo stesso che core applica a issue e commenti
	// (413 body_too_large nel contratto).
	DefaultMaxBodyBytes int64 = 1 << 20
	// DefaultMaxAttachmentBytes è il tetto al corpo dell'upload di un allegato
	// (10 MiB di file, come core), più lo spazio per i delimitatori multipart.
	DefaultMaxAttachmentBytes int64 = 10 << 20
	// multipartOverhead come in core: intestazioni e delimitatori multipart.
	multipartOverhead int64 = 64 << 10
)

// ToCore costruisce l'handler HTTP che instrada le richieste verso core.
// coreURL è la base URL del servizio core (es. http://core:8080); timeout è
// applicato per ciascuna richiesta instradata.
func ToCore(coreURL *url.URL, timeout time.Duration, logger *slog.Logger, opts ...Option) http.Handler {
	return ToService("core", coreURL, timeout, logger, opts...)
}

// ToIdentity costruisce l'handler HTTP che instrada le richieste verso
// identity: stessa logica di ToCore (riscrittura di host, rimozione del
// prefisso /v1, IP del client con i proxy fidati), errore 503 con code
// identity_unavailable.
func ToIdentity(identityURL *url.URL, timeout time.Duration, logger *slog.Logger, opts ...Option) http.Handler {
	return ToService("identity", identityURL, timeout, logger, opts...)
}

// ToService è l'handler generico dietro ToCore e ToIdentity: name è il nome
// del servizio a valle, usato nei log e nel code dell'errore
// ("<name>_unavailable").
func ToService(name string, coreURL *url.URL, timeout time.Duration, logger *slog.Logger, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.now == nil {
		o.now = time.Now
	}
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
			// SetXForwarded scrive X-Forwarded-Proto dallo schema della
			// connessione col gateway (http: Traefik parla in chiaro). Se il
			// peer è un proxy fidato si conserva il valore che ha impostato,
			// così identity sa se il client era su HTTPS.
			if proto := forwardedProto(pr.In, o.trusted); proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
			// Mai fidarsi del valore in ingresso: ogni X-Gitstack-* del client
			// (identità inclusa) si cancella, poi il gateway scrive i propri.
			trust.StripClientHeaders(pr.Out.Header)
			if o.dropCredentials {
				pr.Out.Header.Del("Authorization")
				pr.Out.Header.Del("Cookie")
			}
			if id, ok := trust.FromContext(pr.In.Context()); ok && o.serviceSecret != "" {
				trust.Sign(pr.Out.Header, o.serviceSecret, id, o.now())
				// Il token del job (GIT-372) non è coperto dalla firma: core lo verifica
				// da sé, e solo per l'identità firmata come job.
				if jt := trust.JobTokenFromContext(pr.In.Context()); jt != "" && id.Username == trust.JobUsername {
					pr.Out.Header.Set(trust.HeaderJobToken, jt)
				}
			}
			// L'IP del client viene da X-Forwarded-For solo se la connessione
			// arriva da un proxy fidato (es. Traefik), altrimenti da RemoteAddr.
			if ip := ClientIP(pr.In, o.trusted); ip != "" {
				pr.Out.Header.Set(ClientIPHeader, ip)
			}
			pr.Out.URL.Scheme = coreURL.Scheme
			pr.Out.URL.Host = coreURL.Host
			pr.Out.Host = coreURL.Host
			pr.Out.URL.Path = stripV1(pr.In.URL.Path)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeBodyTooLarge(w, tooLarge.Limit)
				return
			}
			logger.Error("richiesta verso "+name+" non riuscita",
				"request_id", middleware.RequestIDFromContext(r.Context()),
				"path", r.URL.Path,
				"err", err,
			)
			writeError(w, http.StatusServiceUnavailable, name+"_unavailable", "Il servizio "+name+" non ha risposto in tempo.")
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := o.bodyLimit(r)
		if r.ContentLength > limit {
			writeBodyTooLarge(w, limit)
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		rp.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeBodyTooLarge risponde 413 con l'errore del contratto.
func writeBodyTooLarge(w http.ResponseWriter, limit int64) {
	writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
		"Corpo della richiesta oltre il limite di "+strconv.FormatInt(limit, 10)+" byte.")
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
