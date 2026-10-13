package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
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

type fixedVerifier struct{ res identityclient.Result }

func (f fixedVerifier) Verify(context.Context, string, identityclient.Kind) (identityclient.Result, error) {
	return f.res, nil
}

func logTestTable(t *testing.T) *security.Table {
	t.Helper()
	tbl, err := security.NewTable([]security.Route{{
		Method: http.MethodGet, Path: "/things", OperationID: "listThings",
		Credentials: []security.CredentialKind{security.CredentialToken},
		Scopes:      []string{"read:user"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("riga di log non JSON valido: %v\n%s", err, buf.String())
	}
	return entry
}

func TestLogging_UserAndClientIP(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	res := identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "22222222-2222-2222-2222-222222222222", Username: "bot", IsToken: true,
		Scopes: []string{"read:user"}, CredentialID: "44444444-4444-4444-4444-444444444444", TokenName: "ci",
	}}
	secret := "gst_SEGRETISSIMO0123456789"
	handler := Chain(RequestID,
		Logging(logger, WithClientIP(func(*http.Request) string { return "203.0.113.7" })),
		Auth(AuthConfig{Table: logTestTable(t), Verifier: fixedVerifier{res}, Logger: logger}),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/things", nil)
	req.RemoteAddr = "10.42.0.9:5555"
	req.Header.Set("Authorization", "Bearer "+secret)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "COOKIE-SEGRETO"})
	handler.ServeHTTP(httptest.NewRecorder(), req)

	e := logLine(t, &buf)
	if e["client_ip"] != "203.0.113.7" {
		t.Errorf("client_ip = %v, voluto l'IP del client reale", e["client_ip"])
	}
	if e["remote_addr"] != "10.42.0.9:5555" {
		t.Errorf("remote_addr = %v", e["remote_addr"])
	}
	if e["user_id"] != res.Principal.UserID || e["username"] != "bot" || e["token_id"] != res.Principal.CredentialID {
		t.Errorf("utente/token assenti dal log: %v", e)
	}
	for _, secretValue := range []string{secret, "COOKIE-SEGRETO", "Bearer"} {
		if strings.Contains(buf.String(), secretValue) {
			t.Errorf("il log contiene %q: %s", secretValue, buf.String())
		}
	}
}

func TestLogging_DenialReason(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	res := identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "22222222-2222-2222-2222-222222222222", Username: "bot", IsToken: true,
	}}
	handler := Chain(RequestID, Logging(logger),
		Auth(AuthConfig{Table: logTestTable(t), Verifier: fixedVerifier{res}, Logger: logger}),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/things", nil)
	req.Header.Set("Authorization", "Bearer gst_x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, voluto 403", rec.Code)
	}
	e := logLine(t, &buf)
	if e["reason"] != "insufficient_scope" || e["user_id"] != res.Principal.UserID {
		t.Errorf("motivo/utente assenti: %v", e)
	}

	buf.Reset()
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/things", nil))
	if e := logLine(t, &buf); rec.Code != http.StatusUnauthorized || e["reason"] != "missing_credential" {
		t.Errorf("401 senza motivo: %d %v", rec.Code, e)
	}
}
