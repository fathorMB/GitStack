package httpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

type fakeLister struct {
	err   error
	calls int
}

func (f *fakeLister) ReadableResources(context.Context, uuid.UUID) (bool, []uuid.UUID, error) {
	f.calls++
	return false, nil, f.err
}

func getResources(router http.Handler, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/resources", nil)
	if userID != "" {
		trust.Sign(req.Header, trustSecret, trust.Identity{UserID: userID, Username: "alice"}, time.Now())
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// GET /resources non tocca il database (pool nil: un accesso andrebbe in
// panic) quando non può sapere cosa il chiamante legge: mai un elenco non
// filtrato.
func TestListResources_SenzaFiltroNessunElenco(t *testing.T) {
	t.Run("senza identità firmata: 401", func(t *testing.T) {
		router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret, httpserver.WithReadableLister(&fakeLister{}))
		if rec := getResources(router, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("= %d %s, voluto 401", rec.Code, rec.Body.String())
		}
	})
	t.Run("UserID non uuid: 401", func(t *testing.T) {
		l := &fakeLister{}
		router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret, httpserver.WithReadableLister(l))
		rec := getResources(router, "non-uuid")
		if rec.Code != http.StatusUnauthorized || l.calls != 0 {
			t.Fatalf("= %d %s (chiamate %d), voluto 401 senza chiamare identity", rec.Code, rec.Body.String(), l.calls)
		}
	})
	t.Run("client non configurato: 503", func(t *testing.T) {
		router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret)
		rec := getResources(router, creatorUserID)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"identity_unavailable"`) {
			t.Fatalf("= %d %s, voluto 503 identity_unavailable", rec.Code, rec.Body.String())
		}
	})
	for name, err := range map[string]error{"identity non disponibile": identityclient.ErrUnavailable, "errore qualunque": errors.New("boom")} {
		t.Run(name+": 503", func(t *testing.T) {
			l := &fakeLister{err: err}
			router := httpserver.NewRouter(nil, events.NoopPublisher{}, trustSecret, httpserver.WithReadableLister(l))
			rec := getResources(router, creatorUserID)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"identity_unavailable"`) ||
				strings.Contains(rec.Body.String(), "items") || l.calls != 1 {
				t.Fatalf("= %d %s (chiamate %d), voluto 503 senza elenco", rec.Code, rec.Body.String(), l.calls)
			}
		})
	}
}
