package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimit_NoOpLasciaPassare(t *testing.T) {
	called := false
	handler := RateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("RateLimit no-op deve chiamare il gestore successivo senza limitare nulla")
	}
}
