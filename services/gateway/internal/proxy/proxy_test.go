package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestToCore_InstradaRimuovendoPrefissoV1(t *testing.T) {
	var gotPath, gotMethod, gotRequestID string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotRequestID = r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","version":"0.1.0"}`))
	}))
	defer core.Close()

	coreURL, err := url.Parse(core.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	handler := ToCore(coreURL, time.Second, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("X-Request-Id", "req-abc-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotMethod != http.MethodGet {
		t.Errorf("method visto da core = %q, voluto GET", gotMethod)
	}
	if gotPath != "/health" {
		t.Errorf("path visto da core = %q, voluto /health (senza /v1)", gotPath)
	}
	if gotRequestID != "req-abc-123" {
		t.Errorf("request id propagato a core = %q, voluto req-abc-123", gotRequestID)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, voluto 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo della risposta non JSON valido: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("corpo della risposta = %v, voluto status=ok", body)
	}
}

func TestToCore_PreservaQueryEMetodo(t *testing.T) {
	var gotRawQuery string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
	}))
	defer core.Close()

	coreURL, _ := url.Parse(core.URL)
	handler := ToCore(coreURL, time.Second, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/v1/resources?type=repo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotRawQuery != "type=repo" {
		t.Errorf("query vista da core = %q, voluta type=repo", gotRawQuery)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, voluto 201", rec.Code)
	}
}

func TestToCore_PropagaXForwardedForAccodandoIlClient(t *testing.T) {
	var gotXFF string
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer core.Close()

	coreURL, _ := url.Parse(core.URL)
	handler := ToCore(coreURL, time.Second, discardLogger())

	// httptest.NewRequest imposta di default RemoteAddr a "192.0.2.1:1234":
	// il client diretto è quindi 192.0.2.1. La richiesta arriva già con una
	// catena X-Forwarded-For (da Traefik/altri proxy a monte): il gateway
	// deve conservarla e accodarvi il proprio client, non sostituirla.
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if want := "203.0.113.7, 192.0.2.1"; gotXFF != want {
		t.Errorf("X-Forwarded-For visto da core = %q, voluto %q", gotXFF, want)
	}
}

func TestToCore_CoreIrraggiungibileRitornaErroreDelContratto(t *testing.T) {
	// Una URL che punta a una porta chiusa: nessun core in ascolto.
	coreURL, _ := url.Parse("http://127.0.0.1:1")
	handler := ToCore(coreURL, 200*time.Millisecond, discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, voluto 503", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo dell'errore non JSON valido: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("corpo dell'errore non ha forma {error:{...}} del contratto: %v", body)
	}
	if errObj["code"] != "core_unavailable" {
		t.Errorf("error.code = %v, voluto core_unavailable", errObj["code"])
	}
}
