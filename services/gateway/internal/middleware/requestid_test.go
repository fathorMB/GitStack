package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestID_Genera(t *testing.T) {
	var gotFromContext string
	var gotFromRequestHeader string

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromContext = RequestIDFromContext(r.Context())
		gotFromRequestHeader = r.Header.Get(HeaderRequestID)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/qualsiasi", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotFromContext == "" {
		t.Fatal("request id nel contesto è vuoto")
	}
	if gotFromRequestHeader != gotFromContext {
		t.Errorf("header della richiesta = %q, voluto uguale al contesto %q", gotFromRequestHeader, gotFromContext)
	}
	if got := rec.Header().Get(HeaderRequestID); got != gotFromContext {
		t.Errorf("header della risposta = %q, voluto %q", got, gotFromContext)
	}
}

func TestRequestID_RiusaQuelloDelChiamante(t *testing.T) {
	const incoming = "id-del-chiamante-123"
	var got string

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/qualsiasi", nil)
	req.Header.Set(HeaderRequestID, incoming)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got != incoming {
		t.Errorf("request id = %q, voluto quello del chiamante %q", got, incoming)
	}
}

func TestRequestID_DueRichiesteIdDiversi(t *testing.T) {
	ids := map[string]bool{}
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids[RequestIDFromContext(r.Context())] = true
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/qualsiasi", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	if len(ids) != 2 {
		t.Errorf("attesi 2 request id distinti, ottenuti %d: %v", len(ids), ids)
	}
}
