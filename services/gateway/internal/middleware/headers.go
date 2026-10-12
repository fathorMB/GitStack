package middleware

import (
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// SecurityHeaders è un middleware che aggiunge a ogni risposta autenticata le
// intestazioni di sicurezza richieste da ASVS V8.2.1 e V14.4.2:
//
//	Cache-Control: no-store
//	X-Content-Type-Options: nosniff
//
// Non sovrascrive un Cache-Control già presente nel risposta (per esempio
// "private, no-store" messo da un servizio per gli allegati): se il servizio
// ha già impostato Cache-Control, il middleware lo lascia stare.
//
// Le intestazioni non vengono aggiunte alle risposte non autenticate
// (pubbliche, healthz, readyz) per non esporre policy di cache dove non serve.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Solo se il middleware Auth ha inserito un'identità nel contesto:
		// la richiesta è passata la verifica delle credenziali.
		if _, ok := trust.FromContext(r.Context()); !ok {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "no-store")
		}

		next.ServeHTTP(w, r)
	})
}
