package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

const trustSecret = "segreto-di-servizio-di-prova"

// core accetta l'identità solo dal gateway: senza header, con header
// mandati direttamente da un client o con una firma sbagliata risponde 401
// prima di toccare il database (qui il pool è nil: se la richiesta lo
// raggiungesse, il test andrebbe in panic).
func TestRouter_IdentitaSoloDalGateway(t *testing.T) {
	now := time.Unix(1700000000, 0)
	router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret, httpserver.WithClock(func() time.Time { return now }))

	forged := func() http.Header {
		return http.Header{
			trust.HeaderUserID:   {"11111111-1111-1111-1111-111111111111"},
			trust.HeaderUsername: {"root"},
			trust.HeaderScopes:   {"admin:org"},
		}
	}
	wrongSecret := http.Header{}
	trust.Sign(wrongSecret, "un-altro-segreto", trust.Identity{UserID: "u", Username: "alice"}, now)
	expired := http.Header{}
	trust.Sign(expired, trustSecret, trust.Identity{UserID: "u", Username: "alice"}, now.Add(-time.Hour))

	routes := []struct{ method, path string }{
		{"GET", "/resources"},
		{"POST", "/resources"},
		{"GET", "/resources/00000000-0000-0000-0000-000000000000"},
		{"PATCH", "/resources/00000000-0000-0000-0000-000000000000"},
		{"DELETE", "/resources/00000000-0000-0000-0000-000000000000"},
		{"GET", "/qualunque-altra-rotta"},
	}
	headers := map[string]http.Header{
		"nessun header":             nil,
		"header mandati dal client": forged(),
		"firma di un altro segreto": wrongSecret,
		"firma scaduta":             expired,
	}
	for name, hdr := range headers {
		for _, r := range routes {
			req := httptest.NewRequest(r.method, r.path, nil)
			for k, v := range hdr {
				req.Header[k] = v
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
				t.Errorf("%s: %s %s = %d %s, voluto 401 unauthenticated", name, r.method, r.path, rec.Code, rec.Body.String())
			}
		}
	}

	// Il probe di liveness non richiede identità.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, voluto 200", rec.Code)
	}
}
