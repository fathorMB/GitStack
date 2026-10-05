//go:build integration

package httpserver_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// fakeGranter è l'identity finto: registra le chiamate e risponde con err.
type fakeGranter struct {
	err   error
	calls []struct{ resource, user uuid.UUID }
}

func (f *fakeGranter) GrantResourceCreator(_ context.Context, resourceID, userID uuid.UUID) error {
	f.calls = append(f.calls, struct{ resource, user uuid.UUID }{resourceID, userID})
	return f.err
}

// ReadableResources: il finto identity lascia vedere tutto (admin).
func (f *fakeGranter) ReadableResources(context.Context, uuid.UUID) (bool, []uuid.UUID, error) {
	return true, nil, nil
}

func TestCreateResource_GrantAlCreatore(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	g := &fakeGranter{}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret, httpserver.WithCreatorGranter(g))

	rec := postResource(t, router, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /resources = %d %s, voluto 201", rec.Code, rec.Body.String())
	}
	if len(g.calls) != 1 || g.calls[0].user.String() != creatorUserID {
		t.Fatalf("grant chiamato %v, voluto una volta per %s", g.calls, creatorUserID)
	}
	if _, err := store.New(pool).Get(t.Context(), g.calls[0].resource); err != nil {
		t.Fatalf("la risorsa creata deve esistere: %v", err)
	}
}

func TestCreateResource_GrantFallitoAnnullaLaCreazione(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	g := &fakeGranter{err: errors.New("identity giù")}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret, httpserver.WithCreatorGranter(g))

	rec := postResource(t, router, true)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"identity_unavailable"`) {
		t.Fatalf("POST /resources = %d %s, voluto 503 identity_unavailable", rec.Code, rec.Body.String())
	}
	if len(g.calls) != 1 {
		t.Fatalf("grant chiamato %d volte, voluto 1", len(g.calls))
	}
	// Nessuna risorsa orfana.
	if _, err := store.New(pool).Get(t.Context(), g.calls[0].resource); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("la risorsa deve essere stata cancellata, Get = %v", err)
	}
	_, total, err := store.New(pool).List(t.Context(), nil, nil, 1, 20)
	if err != nil || total != 0 {
		t.Fatalf("List = %d, %v; voluto 0 risorse", total, err)
	}
}
