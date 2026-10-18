package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecover_Generic500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Chain(RequestID, Logging(logger), Recover(logger))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("DETTAGLIO-INTERNO-riservato")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, voluto 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "DETTAGLIO-INTERNO") || strings.Contains(rec.Body.String(), "goroutine") {
		t.Errorf("il corpo svela dettagli: %s", rec.Body.String())
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "internal_error" {
		t.Errorf("corpo non è l'errore generico del contratto: %q", rec.Body.String())
	}

	// Due righe: il panic con lo stack e la richiesta con status 500.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("righe di log = %d, volute 2:\n%s", len(lines), buf.String())
	}
	var p, q map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &p)
	_ = json.Unmarshal([]byte(lines[1]), &q)
	if p["event"] != "security.panic" || !strings.Contains(p["stack"].(string), "recover_test.go") {
		t.Errorf("riga del panic senza stack: %v", p)
	}
	if p["request_id"] == "" || p["request_id"] != q["request_id"] {
		t.Errorf("request id assente o diverso: %v / %v", p["request_id"], q["request_id"])
	}
	if q["status"] != float64(500) {
		t.Errorf("la riga di richiesta ha status %v, voluto 500", q["status"])
	}
}

func TestRecover_AbortHandlerRilanciato(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("http.ErrAbortHandler va rilanciato")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
