package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogging_UnaRigaJSONConCampiAttesi(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	chain := Chain(RequestID, Logging(logger))
	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/resources", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	line := bytes.TrimSpace(buf.Bytes())
	if len(line) == 0 {
		t.Fatal("nessuna riga di log prodotta")
	}

	var entry map[string]any
	if err := json.Unmarshal(line, &entry); err != nil {
		t.Fatalf("la riga di log non è JSON valido: %v\nriga: %s", err, line)
	}

	for _, field := range []string{"request_id", "method", "path", "status", "duration_ms"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("campo %q assente nel log: %v", field, entry)
		}
	}
	if entry["method"] != http.MethodPost {
		t.Errorf("method = %v, voluto POST", entry["method"])
	}
	if entry["path"] != "/v1/resources" {
		t.Errorf("path = %v, voluto /v1/resources", entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, voluto 201", entry["status"])
	}
	if entry["request_id"] == "" {
		t.Error("request_id è vuoto: RequestID deve girare prima di Logging")
	}
}

func TestLogging_StatusDefaultOkSeNonScritto(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Non chiama WriteHeader né Write: il default deve essere 200, come
		// fa net/http quando nessuno scrive esplicitamente lo status.
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("riga di log non JSON valido: %v", err)
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, voluto 200 di default", entry["status"])
	}
}
