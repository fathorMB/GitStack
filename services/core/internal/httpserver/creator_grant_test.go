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

const creatorUserID = "11111111-1111-1111-1111-111111111111"

func postResource(t *testing.T, router http.Handler, signed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/resources", strings.NewReader(`{"type":"repo","name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	if signed {
		trust.Sign(req.Header, trustSecret, trust.Identity{UserID: creatorUserID, Username: "alice"}, time.Now())
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// Senza identity configurata (GITSTACK_IDENTITY_URL vuota) POST /resources
// risponde 503 prima di toccare il database (pool nil: un accesso andrebbe
// in panic): mai una risorsa senza grant.
func TestCreateResource_IdentityNonConfigurata503(t *testing.T) {
	router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret)
	rec := postResource(t, router, true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"identity_unavailable"`) {
		t.Fatalf("POST /resources = %d %s, voluto 503 identity_unavailable", rec.Code, rec.Body.String())
	}
}
