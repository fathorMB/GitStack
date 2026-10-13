package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder intercetta lo status code scritto dal gestore a valle: gli
// http.ResponseWriter non lo espongono altrimenti.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

type logInfoKey struct{}

// logInfo raccoglie i dati che solo i middleware a valle di Logging
// conoscono (utente, token, motivo di un rifiuto): Logging lo mette nel
// contesto e li legge a risposta finita.
type logInfo struct {
	userID, username, tokenID, reason string
}

// setLogActor registra utente e token autenticati per la riga di log. Mai il
// valore della credenziale: solo gli identificatori.
func setLogActor(ctx context.Context, userID, username, tokenID string) {
	if li, ok := ctx.Value(logInfoKey{}).(*logInfo); ok {
		li.userID, li.username, li.tokenID = userID, username, tokenID
	}
}

// setLogReason registra il motivo di un 401/403/429 per la riga di log.
func setLogReason(ctx context.Context, reason string) {
	if li, ok := ctx.Value(logInfoKey{}).(*logInfo); ok {
		li.reason = reason
	}
}

// LoggingOption configura Logging.
type LoggingOption func(*loggingConfig)

type loggingConfig struct {
	clientIP func(*http.Request) string
}

// WithClientIP imposta come si ricava l'IP del client reale (di norma
// proxy.ClientIP con i proxy fidati): senza, client_ip è l'indirizzo del peer.
func WithClientIP(f func(*http.Request) string) LoggingOption {
	return func(c *loggingConfig) { c.clientIP = f }
}

// Logging costruisce il middleware di log strutturato: una riga JSON per
// richiesta, con request id, metodo, percorso, status, durata, IP del client
// e, se la richiesta è autenticata, utente e id del token (mai la
// credenziale). Per 401/403/429 c'è anche il motivo. Va dopo RequestID nella
// catena (deve leggere il request id già impostato nel contesto).
func Logging(logger *slog.Logger, opts ...LoggingOption) func(http.Handler) http.Handler {
	var cfg loggingConfig
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			li := &logInfo{}
			r = r.WithContext(context.WithValue(r.Context(), logInfoKey{}, li))

			next.ServeHTTP(rec, r)

			ip := r.RemoteAddr
			if cfg.clientIP != nil {
				if v := cfg.clientIP(r); v != "" {
					ip = v
				}
			}
			attrs := []any{
				"request_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"client_ip", ip,
			}
			if li.userID != "" {
				attrs = append(attrs, "user_id", li.userID, "username", li.username)
			}
			if li.tokenID != "" {
				attrs = append(attrs, "token_id", li.tokenID)
			}
			if li.reason != "" {
				attrs = append(attrs, "reason", li.reason)
			}
			logger.Info("richiesta gestita", attrs...)
		})
	}
}
