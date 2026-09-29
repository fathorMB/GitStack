package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct {
	err error
}

func (f *fakePinger) Ping(_ context.Context) error {
	return f.err
}

func TestHealthz_AlwaysOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	Healthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("corpo: %s", rec.Body.String())
	}
}

func TestReadyz_Unavailable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler := Readyz(&fakePinger{err: context.DeadlineExceeded})
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, voluto 503", rec.Code)
	}
}

func TestReadyz_Available(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler := Readyz(&fakePinger{err: nil})
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
}

func TestRouter_RoutesHealthAndAPI(t *testing.T) {
	// Finto handler API che risponde 418 (non è un teapot, è solo un test).
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	router := NewRouter(nil, api)

	// /healthz risponde 200 dal nostro handler.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz: status = %d, voluto 200", rec.Code)
	}

	// /users/x passa all'api (418).
	req = httptest.NewRequest(http.MethodGet, "/users/x", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("/users/x: status = %d, voluto 418", rec.Code)
	}
}
