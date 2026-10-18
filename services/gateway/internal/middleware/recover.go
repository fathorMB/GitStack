package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// writeTracker ricorda se la risposta è già partita: dopo, un 500 non si può
// più scrivere.
type writeTracker struct {
	http.ResponseWriter
	wrote bool
}

func (t *writeTracker) WriteHeader(status int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *writeTracker) Write(b []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(b)
}

// Unwrap lascia a http.ResponseController l'accesso al writer sottostante
// (Flush, Hijack).
func (t *writeTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// Recover intercetta un panic del gestore a valle: risponde 500 generico
// (nessun dettaglio al client) e scrive nel log l'errore con lo stack e il
// request id. Va dentro Logging, così la riga di richiesta riporta 500.
// http.ErrAbortHandler non è un guasto: viene rilanciato come vuole net/http.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &writeTracker{ResponseWriter: w}
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.Error("panic nel gestore della richiesta",
					"event", "security.panic",
					"request_id", RequestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				setLogReason(r.Context(), "panic")
				if !tw.wrote {
					writeAuthError(tw, http.StatusInternalServerError, "internal_error", "Errore interno.", nil)
				}
			}()
			next.ServeHTTP(tw, r)
		})
	}
}
