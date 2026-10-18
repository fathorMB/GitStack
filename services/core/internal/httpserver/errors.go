package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// writeError scrive un errore nel formato unico del contratto (schema
// Error), come services/gateway/internal/proxy.writeError.
func writeError(w http.ResponseWriter, status int, code, message string) {
	var body openapi.Error
	body.Error.Code = code
	body.Error.Message = message

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeErrorDetails è writeError con i dettagli strutturati dell'errore.
func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	var body openapi.Error
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Details = &details

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
