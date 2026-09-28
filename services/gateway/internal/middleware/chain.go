// Package middleware contiene la catena di middleware HTTP del gateway:
// request id, log strutturati e i punti di aggancio per verifica token e
// rate limiting (no-op in M-01, implementati in M-02).
package middleware

import "net/http"

// Middleware è un middleware HTTP standard. È un alias (non un tipo nuovo)
// così un valore di questo tipo si passa senza conversioni a qualunque API
// che si aspetti un func(http.Handler) http.Handler, incluso
// openapi.MiddlewareFunc generato da oapi-codegen.
type Middleware = func(http.Handler) http.Handler

// Chain compone più middleware in uno solo. Il primo argomento è il più
// esterno: esegue per primo all'arrivo della richiesta e per ultimo
// all'uscita della risposta. Esempio: Chain(RequestID, Logging(log)) fa sì
// che RequestID assegni l'id prima che Logging lo possa registrare.
func Chain(mws ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		h := final
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}
