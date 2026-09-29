package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/config"
)

// Dal router reale del binario (buildRouter, lo stesso di serve) le rotte di
// token, chiavi SSH e /internal non rispondono più 501.
func TestBuildRouter_RotteMontate(t *testing.T) {
	const secret = "s3gr3to-di-servizio-xyz"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	for _, tc := range []struct {
		name   string
		secret string
		method string
		path   string
		auth   string
	}{
		{"tokens senza sessione", secret, "GET", "/user/tokens", ""},
		{"ssh-keys senza sessione", secret, "GET", "/user/ssh-keys", ""},
		{"verify senza segreto nella richiesta", secret, "POST", "/internal/verify", ""},
		{"verify con segreto di servizio non configurato", "", "POST", "/internal/verify", "Bearer x"},
		{"ssh-keys interno senza segreto", secret, "GET", "/internal/ssh-keys/SHA256:abc", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{ServiceSecret: tc.secret}
			router := buildRouter(cfg, nil, &auth.Service{}, logger)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s = %d, voluto 401 (non 501/404)", tc.method, tc.path, rec.Code)
			}
		})
	}

	if strings.Contains(logs.String(), secret) {
		t.Errorf("il segreto di servizio è comparso nei log: %s", logs.String())
	}
}

// Con il segreto giusto /internal/verify passa serviceAuth e arriva al
// gestore: un corpo non valido dà 400, non 401/501.
func TestBuildRouter_InternalVerifyConSegreto(t *testing.T) {
	cfg := config.Config{ServiceSecret: "s3gr3to"}
	router := buildRouter(cfg, nil, &auth.Service{}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	req := httptest.NewRequest("POST", "/internal/verify", strings.NewReader("non json"))
	req.Header.Set("Authorization", "Bearer s3gr3to")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented || rec.Code == http.StatusUnauthorized || rec.Code == http.StatusNotFound {
		t.Errorf("POST /internal/verify = %d", rec.Code)
	}
}
