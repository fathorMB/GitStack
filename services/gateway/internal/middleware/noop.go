package middleware

import "net/http"

// Auth è il punto di aggancio per la verifica del token: in M-01 non fa
// nulla (no-op), lascia passare ogni richiesta. M-02 la sostituisce con la
// verifica reale (chiamata a identity o validazione locale del token), senza
// dover toccare la catena in cui è inserita (vedi httpserver.NewRouter).
func Auth(next http.Handler) http.Handler {
	return next
}

// RateLimit è il punto di aggancio per il rate limiting: in M-01 non fa
// nulla (no-op), lascia passare ogni richiesta. M-02 la sostituisce con un
// limitatore reale (per token o per IP), senza dover toccare la catena in
// cui è inserita.
func RateLimit(next http.Handler) http.Handler {
	return next
}
