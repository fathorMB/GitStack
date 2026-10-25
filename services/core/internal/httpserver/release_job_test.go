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

// jobIdentity è l'identità che il gateway firma per un token del job (GIT-372).
func jobIdentity() trust.Identity {
	return trust.Identity{UserID: "00000000-0000-0000-0000-000000000000", Username: trust.JobUsername, Scopes: []string{trust.JobScope}}
}

// GIT-372, seconda barriera: l'identità del job passa solo per create e upload delle release.
// Ogni altra rotta risponde 403 prima di arrivare agli handler (il pool è nil: se la
// richiesta arrivasse a un handler dei repo, il test andrebbe in panic).
func TestRouter_IdentitaDelJobSoloSuCreateEUploadDelleRelease(t *testing.T) {
	now := time.Unix(1700000000, 0)
	router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret, httpserver.WithClock(func() time.Time { return now }))
	const rel = "/repos/alice/app/releases/00000000-0000-0000-0000-0000000000aa"

	forbidden := []struct{ method, path string }{
		{"GET", "/repos/alice/app"},
		{"PATCH", "/repos/alice/app"},
		{"DELETE", "/repos/alice/app"},
		{"POST", "/repos"},
		{"POST", "/repos/alice/app/issues"},
		{"GET", "/repos/alice/app/issues"},
		{"POST", "/repos/alice/app/issues/1/comments"},
		{"GET", "/repos/alice/app/releases"},
		{"GET", rel},
		{"PATCH", rel},
		{"DELETE", rel},
		{"GET", rel + "/assets/00000000-0000-0000-0000-0000000000bb/download"},
		{"POST", rel + "/assets/00000000-0000-0000-0000-0000000000bb/download"},
		{"POST", "/repos/alice/app/releases/x/y/assets"},
		{"GET", "/resources"},
		{"POST", "/resources"},
		{"PUT", "/repos/alice/app/ci/secrets/X"},
		{"POST", "/repos/alice/app/workflows/ci.yml/dispatches"},
	}
	for _, tc := range forbidden {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		trust.Sign(req.Header, trustSecret, jobIdentity(), now)
		req.Header.Set(trust.HeaderJobToken, "gsj_qualsiasi")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s con identità del job = %d %s, voluto 403", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Le due rotte consentite arrivano all'handler, che senza token del job valido risponde 401.
	for _, tc := range []struct{ method, path string }{
		{"POST", "/repos/alice/app/releases"},
		{"POST", rel + "/assets?name=a.zip"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		trust.Sign(req.Header, trustSecret, jobIdentity(), now) // senza X-Gitstack-Job-Token
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s senza token del job = %d %s, voluto 401", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// L'identità del job con scope o UUID diversi dal gateway è respinta anche sulle rotte consentite.
	for name, id := range map[string]trust.Identity{
		"scope in più": {UserID: jobIdentity().UserID, Username: trust.JobUsername, Scopes: []string{trust.JobScope, "write:resource"}},
		"senza scope":  {UserID: jobIdentity().UserID, Username: trust.JobUsername},
		"utente vero":  {UserID: "11111111-1111-1111-1111-111111111111", Username: trust.JobUsername, Scopes: []string{trust.JobScope}},
	} {
		req := httptest.NewRequest("POST", "/repos/alice/app/releases", strings.NewReader("{}"))
		trust.Sign(req.Header, trustSecret, id, now)
		req.Header.Set(trust.HeaderJobToken, "gsj_qualsiasi")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, voluto 403", name, rec.Code)
		}
	}
}
