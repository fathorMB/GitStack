package gitclient

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Il corpo è quello di writeErr del servizio git: {"error":{"code","message"}}.
func TestReadJSON_CodiciDiGit(t *testing.T) {
	tests := []struct {
		name, body, code, msg string
		status                int
	}{
		{"blame_unavailable", `{"error":{"code":"blame_unavailable","message":"file binario"}}`, "blame_unavailable", "file binario", 400},
		{"invalid_ref", `{"error":{"code":"invalid_ref","message":"ref non valido"}}`, "invalid_ref", "ref non valido", 400},
		{"ref_not_found", `{"error":{"code":"ref_not_found","message":"ref assente"}}`, "ref_not_found", "ref assente", 404},
		{"corpo vuoto 400", ``, "invalid_request", "", 400},
		{"corpo vuoto 404", ``, "not_found", "", 404},
		{"senza codice", `{"error":{"message":"x"}}`, "invalid_request", "", 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.ReadJSON(t.Context(), caller, uuid.New(), "blame", nil)
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("atteso *APIError, ho %v", err)
			}
			if ae.Status != tc.status || ae.Code != tc.code || ae.Message != tc.msg {
				t.Errorf("ho %+v", ae)
			}
		})
	}
}
