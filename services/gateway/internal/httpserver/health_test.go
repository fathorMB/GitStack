package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
)

func TestHealthz_RitornaOkESchemaDelContratto(t *testing.T) {
	rec := httptest.NewRecorder()
	healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non JSON valido: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, voluto ok", body.Status)
	}
	if body.Version == "" {
		t.Error("version è vuota")
	}
}

func TestReadyz_Unitario(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	upURL, _ := url.Parse(up.URL)
	handler := readyz(upURL, &http.Client{Timeout: time.Second}, time.Second)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("con core su: status = %d, voluto 200", rec.Code)
	}

	unreachable, _ := url.Parse("http://127.0.0.1:1")
	handlerGiu := readyz(unreachable, &http.Client{Timeout: 200 * time.Millisecond}, 200*time.Millisecond)

	rec2 := httptest.NewRecorder()
	handlerGiu(rec2, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("con core giù: status = %d, voluto 503", rec2.Code)
	}
}

func TestMeta_VersioneDellInstallazione(t *testing.T) {
	cfg := config.Config{CoreURL: &url.URL{Scheme: "http", Host: "core:8080"}, CoreTimeout: time.Second, Version: "v1.2.3"}
	rec := httptest.NewRecorder()
	NewRouter(cfg, slog.New(slog.DiscardHandler)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		ServerVersion string `json:"server_version"`
		APIVersion    string `json:"api_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ServerVersion != "v1.2.3" || body.APIVersion != GatewayVersion {
		t.Errorf("corpo = %+v", body)
	}
}
