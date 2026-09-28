package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// HeaderRequestID è l'header HTTP usato per propagare il request id: letto
// dalla richiesta in arrivo (se presente, es. impostato da Traefik) e
// impostato sulla risposta al chiamante e sulla richiesta instradata verso
// core, così un'unica richiesta si segue nei log di tutti i servizi.
const HeaderRequestID = "X-Request-Id"

type contextKey int

const requestIDKey contextKey = iota

// RequestID è il middleware che assicura a ogni richiesta un request id:
// riusa quello del chiamante se presente in HeaderRequestID, altrimenti ne
// genera uno nuovo. Lo scrive nel contesto della richiesta (leggibile con
// RequestIDFromContext), nell'header della richiesta stessa (in modo che il
// proxy verso core lo trovi già pronto da propagare) e nell'header della
// risposta.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = newRequestID()
		}
		r.Header.Set(HeaderRequestID, id)
		w.Header().Set(HeaderRequestID, id)

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext ritorna il request id della richiesta corrente, o
// stringa vuota se il middleware RequestID non è nella catena.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// newRequestID genera un id casuale a 16 byte in esadecimale: non è un UUID
// (nessuna dipendenza in più solo per questo), ma ha la stessa unicità
// praticamente garantita per un identificatore di richiesta.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand non fallisce in pratica su piattaforme supportate; in
		// caso contrario, un id degradato è meglio di un panic in un
		// middleware.
		return "degraded-request-id"
	}
	return hex.EncodeToString(b[:])
}
