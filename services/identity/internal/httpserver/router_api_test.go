package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
)

// Il router monta l'handler reale di httpapi: /auth/* e /users/* rispondono
// dal contratto (401 senza sessione, senza toccare il database), non 404.
func TestRouter_MontaHttpapiReale(t *testing.T) {
	api := httpapi.New(&auth.Service{}, nil)
	router := NewRouter(nil, api)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/auth/session"},
		{"POST", "/auth/logout"},
		{"GET", "/users"},
		{"GET", "/users/alice"},
		{"PUT", "/users/alice/password"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, voluto 401", tc.method, tc.path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d", rec.Code)
	}
}
