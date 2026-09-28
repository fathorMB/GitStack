package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz_AlwaysOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
}
