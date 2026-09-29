package middleware

import "net/http"

// RateLimit è il punto di aggancio per il rate limiting: oggi non fa nulla
// (no-op), lascia passare ogni richiesta. Una milestone successiva la
// sostituisce con un limitatore reale (per token o per IP), senza dover
// toccare la catena in cui è inserita.
func RateLimit(next http.Handler) http.Handler {
	return next
}
