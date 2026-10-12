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
// Non sovrascrive un Cache-Control già presente nella risposta (per esempio
// "private, no-store" messo da un servizio per gli allegati): se il servizio
// ha già impostato Cache-Control, il middleware lo lascia stare.
//
// Il middleware non scrive mai le intestazioni prima di next.ServeHTTP,
// perché con ReverseProxy le intestazioni del servizio verrebbero accodate
// (.Add) a quelle già impostate, producendo valori doppi. Invece avvolge il
// ResponseWriter e applica le intestazioni al primo WriteHeader/Write, quando
// le intestazioni del servizio sono già nella mappa.
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

		next.ServeHTTP(&headerWriter{ResponseWriter: w}, r)
	})
}

// headerWriter avvolge un http.ResponseWriter e scrive le intestazioni di
// sicurezza al primo WriteHeader o Write, dopo che le intestazioni del
// servizio a valle sono già state depositate dal proxy.
type headerWriter struct {
	http.ResponseWriter
	done bool
}

// WriteHeader scrive le intestazioni di sicurezza se non è ancora stato
// scritto nulla, poi inoltra al writer sottostante.
func (h *headerWriter) WriteHeader(statusCode int) {
	if !h.done {
		h.applyHeaders()
		h.done = true
	}
	h.ResponseWriter.WriteHeader(statusCode)
}

// Write scrive le intestazioni di sicurezza se non è ancora stato scritto
// nulla (il primo Write implica WriteHeader(http.StatusOK)), poi inoltra.
func (h *headerWriter) Write(b []byte) (int, error) {
	if !h.done {
		h.applyHeaders()
		h.done = true
	}
	return h.ResponseWriter.Write(b)
}

// Flush inoltra Flush al writer sottostante, applicando le intestazioni se
// necessario (utile per streaming e ResponseController).
func (h *headerWriter) Flush() {
	if !h.done {
		h.applyHeaders()
		h.done = true
	}
	if flusher, ok := h.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap restituisce il ResponseWriter sottostante, necessario per
// http.ResponseController che lo usa per trovare l'interfaccia
// http.Flusher.
func (h *headerWriter) Unwrap() http.ResponseWriter {
	return h.ResponseWriter
}

// applyHeaders imposta Cache-Control e X-Content-Type-Options solo se non
// sono già state impostate dal servizio a valle.
func (h *headerWriter) applyHeaders() {
	if h.ResponseWriter.Header().Get("Cache-Control") == "" {
		h.ResponseWriter.Header().Set("Cache-Control", "no-store")
	}
	h.ResponseWriter.Header().Set("X-Content-Type-Options", "nosniff")
}
