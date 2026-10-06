// Package cmdutil raccoglie quello che i comandi di gs condividono: flussi
// di I/O, Factory, errori tipizzati con i codici di uscita e la conferma G8.
package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
)

// Codici di uscita di gs (G3).
const (
	ExitOK            = 0
	ExitGeneric       = 1 // errore generico
	ExitUsage         = 2 // uso errato: flag, argomenti, conferma mancante
	ExitUnauthorized  = 4 // non autenticato (401)
	ExitForbidden     = 5 // permesso negato (403, insufficient_scope)
	ExitNotFound      = 6 // non trovato (404)
	codeInsufficient  = "insufficient_scope"
	codeUnauthorized  = "unauthenticated"
	codeNotFound      = "not_found"
	codeForbidden     = "forbidden"
	codeUsageError    = "usage_error"
	codeGenericError  = "error"
	codeNotAuthnLocal = "not_authenticated"
)

// ErrSilent indica che l'output è già stato scritto (per esempio l'elenco dei
// campi di --json): l'uscita è 0 e non si stampa niente altro.
var ErrSilent = errors.New("silent")

// ErrCancelled è l'errore di un'operazione annullata dall'utente.
var ErrCancelled = errors.New("operazione annullata")

// APIError è una risposta non-2xx dell'API, nel formato errore unico di
// api/openapi.yaml (components.schemas.Error: error.code, error.message).
type APIError struct {
	Status  int    // stato HTTP
	Code    string // error.code, vuoto se il corpo non è nel formato unico
	Message string // error.message
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d, %s)", msg, e.Status, e.Code)
	}
	return fmt.Sprintf("%s (HTTP %d)", msg, e.Status)
}

// UsageError è un uso errato del comando (exit 2).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// UsageErrorf costruisce un UsageError.
func UsageErrorf(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// ExitError porta un codice di uscita esplicito (per esempio 4 quando manca
// il token in locale).
type ExitError struct {
	Code    int
	ErrCode string // codice stabile per l'errore JSON
	Err     error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// NotAuthenticatedError: nessuna istanza o nessun token configurati (exit 4).
func NotAuthenticatedError(format string, args ...any) error {
	return &ExitError{Code: ExitUnauthorized, ErrCode: codeNotAuthnLocal, Err: fmt.Errorf(format, args...)}
}

// ExitCode mappa un errore sul codice di uscita di gs. nil e ErrSilent → 0.
func ExitCode(err error) int {
	if err == nil || errors.Is(err, ErrSilent) {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return statusExit(ae.Status, ae.Code)
	}
	return ExitGeneric
}

func statusExit(status int, code string) int {
	switch {
	case code == codeInsufficient:
		return ExitForbidden
	case status == http.StatusUnauthorized:
		return ExitUnauthorized
	case status == http.StatusForbidden:
		return ExitForbidden
	case status == http.StatusNotFound:
		return ExitNotFound
	}
	return ExitGeneric
}

// ErrorCode è il codice stabile (leggibile da macchina) di un errore, per
// l'errore JSON su stderr: quello dell'API se c'è, altrimenti uno di gs.
func ErrorCode(err error) string {
	var ae *APIError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.ErrCode != "" {
		return ee.ErrCode
	}
	switch ExitCode(err) {
	case ExitUsage:
		return codeUsageError
	case ExitUnauthorized:
		return codeUnauthorized
	case ExitForbidden:
		return codeForbidden
	case ExitNotFound:
		return codeNotFound
	}
	return codeGenericError
}
