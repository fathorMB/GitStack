//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-347 (v3-a, H6): il modo di accesso dell'app. Il default è il login GitStack; «senza login»
// lo imposta solo chi ha admin sul repo.

type kickCounter struct{ n atomic.Int32 }

func (k *kickCounter) Kick()                                    { k.n.Add(1) }
func (k *kickCounter) Restart(context.Context, store.App) error { return nil }
func (k *kickCounter) Scale(context.Context, store.App) error   { return nil }

func TestAppsAccess_LoginDiDefaultSenzaLoginSoloDaAdmin(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base := &issuesEnv{t: t, pool: pool, id: id}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	kick := &kickCounter{}
	base.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now), httpserver.WithAppController(kick))
	e := &appsEnv{issuesEnv: base, clock: clock, git: git}

	repoID := e.repo("handbook", true) // internal: bob e carol leggono
	e.id.grantWrite(repoID, "bob")
	e.repo("riservato", false)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	path := "/repos/alice/handbook/apps/web/access"

	// Una app nuova è dietro il login GitStack: le intestazioni firmate sono attive.
	rec := e.do(http.MethodGet, path, "carol", "")
	e.want(rec, http.StatusOK, "")
	var acc openapi.AppAccess
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil {
		t.Fatal(err)
	}
	if acc.Mode != openapi.AppAccessModeLogin || acc.IdentityHeaders == nil || !*acc.IdentityHeaders || acc.PublicAppsEnabled ||
		acc.Address == nil || *acc.Address != "https://web-handbook-alice.apps.example.com" {
		t.Fatalf("accesso di default: %s", rec.Body.String())
	}

	// «Senza login» non lo imposta chi ha solo write né chi ha solo read; il repo che non si legge è 404.
	body := `{"mode":"none"}`
	e.want(e.do(http.MethodPut, path, "bob", body), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, path, "carol", body), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, "/repos/alice/riservato/apps/web/access", "carol", body), http.StatusNotFound, "")
	if a := e.app(e.do(http.MethodGet, "/repos/alice/handbook/apps/web", "carol", "")); a.Access != openapi.AppAccessModeLogin {
		t.Fatalf("dopo i rifiuti l'app resta dietro il login: %s", a.Access)
	}
	if kick.n.Load() != 0 {
		t.Fatal("un rifiuto non deve toccare la rotta")
	}

	// Scelta esplicita dell'admin del repo.
	rec = e.do(http.MethodPut, path, "alice", body)
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || acc.Mode != openapi.AppAccessModeNone || *acc.IdentityHeaders {
		t.Fatalf("senza login: %s", rec.Body.String())
	}
	if a := e.app(e.do(http.MethodGet, "/repos/alice/handbook/apps/web", "carol", "")); a.Access != openapi.AppAccessModeNone {
		t.Fatalf("app: %s", a.Access)
	}
	if kick.n.Load() != 1 {
		t.Fatalf("il controller va svegliato per riallineare la rotta: %d", kick.n.Load())
	}
	// Non crea una versione.
	if l := e.versions("/repos/alice/handbook/apps/web/versions", "alice"); len(l.Items) != 1 {
		t.Fatalf("l'accesso non crea versioni: %d", len(l.Items))
	}

	// Pubblica: serve l'abilitazione dell'amministratore (doppio consenso), che non c'è.
	e.want(e.do(http.MethodPut, path, "alice", `{"mode":"public"}`), http.StatusUnprocessableEntity, "public_apps_disabled")
	e.want(e.do(http.MethodPut, path, "alice", `{"mode":"anonimo"}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPut, path, "alice", `{"mode":"login","x":1}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/nessuna/access", "alice", body), http.StatusNotFound, "")

	// Si torna al login.
	rec = e.do(http.MethodPut, path, "alice", `{"mode":"login"}`)
	e.want(rec, http.StatusOK, "")
	if a := e.app(e.do(http.MethodGet, "/repos/alice/handbook/apps/web", "carol", "")); a.Access != openapi.AppAccessModeLogin {
		t.Fatalf("app: %s", a.Access)
	}
}
