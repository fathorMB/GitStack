//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// fakeAccess risponde come POST /internal/permissions/user-access, per utente.
type fakeAccess map[uuid.UUID]identityclient.UserAccessResult

func (f fakeAccess) UserAccess(_ context.Context, id uuid.UUID) (identityclient.UserAccessResult, error) {
	r, ok := f[id]
	if !ok {
		return identityclient.UserAccessResult{}, identityclient.ErrNotFound
	}
	return r, nil
}

func decodeAccess(t *testing.T, rec *httptest.ResponseRecorder) openapi.UserAccessList {
	t.Helper()
	var l openapi.UserAccessList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, rec.Body.String())
	}
	return l
}

func TestUserAccess(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	e.id.sysAdmin[carolID] = true // carol è l'amministratore dell'installazione

	direct := e.create("alice", `{"owner":"alice","name":"direct"}`)
	viaTeam := e.create("alice", `{"owner":"acme","name":"viateam"}`)
	internal := e.create("alice", `{"owner":"acme","name":"internal","visibility":"internal"}`)
	summed := e.create("alice", `{"owner":"acme","name":"summed","visibility":"internal"}`)
	hidden := e.create("alice", `{"owner":"alice","name":"hidden"}`)
	gone := e.create("alice", `{"owner":"alice","name":"gone"}`)
	e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNoContent)

	bob := uuid.MustParse(bobID)
	src := func(kind, role, org, team string) identityclient.AccessSource {
		return identityclient.AccessSource{Kind: kind, Role: role, Organization: org, Team: team}
	}
	access := fakeAccess{bob: {Items: []identityclient.ResourceAccess{
		{ResourceID: uuid.UUID(*direct.Id), Role: "write", Sources: []identityclient.AccessSource{src("direct", "write", "", "")}},
		{ResourceID: uuid.UUID(*viaTeam.Id), Role: "read", Sources: []identityclient.AccessSource{src("team", "read", "acme", "web")}},
		{ResourceID: uuid.UUID(*internal.Id), Role: "read", Sources: []identityclient.AccessSource{src("internal", "read", "", "")}},
		{ResourceID: uuid.UUID(*summed.Id), Role: "write", Sources: []identityclient.AccessSource{
			src("team", "write", "acme", "web"), src("direct", "read", "", ""), src("internal", "read", "", "")}},
		// repo eliminato e risorsa che non è un repo: non compaiono.
		{ResourceID: uuid.UUID(*gone.Id), Role: "admin", Sources: []identityclient.AccessSource{src("owner", "admin", "", "")}},
		{ResourceID: uuid.New(), Role: "read", Sources: []identityclient.AccessSource{src("direct", "read", "", "")}},
	}}, uuid.MustParse(aliceID): {Admin: true}}
	_ = hidden
	r := httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(e.git),
		httpserver.WithUserAccess(access))
	do := func(user, path string) *httptest.ResponseRecorder {
		e.router = r
		return e.do(http.MethodGet, path, user, "")
	}

	t.Run("solo_amministratore", func(t *testing.T) {
		for _, u := range []string{"alice", "bob"} { // anche per se stessi
			if rec := do(u, "/users/bob/access"); rec.Code != http.StatusForbidden {
				t.Errorf("%s: %d %s", u, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("ruolo_e_provenienza", func(t *testing.T) {
		rec := do("carol", "/users/bob/access")
		e.want(rec, http.StatusOK)
		l := decodeAccess(t, rec)
		if l.Total != 4 || len(l.Items) != 4 {
			t.Fatalf("total=%d items=%d: %s", l.Total, len(l.Items), rec.Body.String())
		}
		by := map[string]openapi.UserAccessItem{}
		for _, it := range l.Items {
			by[it.FullName] = it
		}
		if it := by["alice/direct"]; it.Role != openapi.ResourceRoleWrite || len(it.Sources) != 1 || it.Sources[0].Kind != openapi.AccessSourceKindDirect {
			t.Errorf("diretto: %+v", it)
		}
		if it := by["acme/viateam"]; it.Role != openapi.ResourceRoleRead || len(it.Sources) != 1 || it.Sources[0].Kind != openapi.AccessSourceKindTeam ||
			it.Sources[0].Organization == nil || *it.Sources[0].Organization != "acme" || it.Sources[0].Team == nil || *it.Sources[0].Team != "web" {
			t.Errorf("via team: %+v", it)
		}
		if it := by["acme/internal"]; it.Role != openapi.ResourceRoleRead || len(it.Sources) != 1 || it.Sources[0].Kind != openapi.AccessSourceKindInternal ||
			it.Visibility == nil || *it.Visibility != openapi.RepoVisibilityInternal {
			t.Errorf("internal: %+v", it)
		}
		if it := by["acme/summed"]; it.Role != openapi.ResourceRoleWrite || len(it.Sources) != 3 {
			t.Errorf("fonti sommate: %+v", it)
		}
		if _, ok := by["alice/gone"]; ok {
			t.Error("repo eliminato elencato")
		}
		if _, ok := by["alice/hidden"]; ok {
			t.Error("repo senza accesso elencato")
		}
	})

	t.Run("paginazione", func(t *testing.T) {
		l := decodeAccess(t, do("carol", "/users/bob/access?perPage=3&page=2"))
		if l.Total != 4 || len(l.Items) != 1 || l.Page != 2 || l.PerPage != 3 {
			t.Errorf("pagina 2: %+v", l)
		}
		if rec := do("carol", "/users/bob/access?page=0"); rec.Code != http.StatusBadRequest {
			t.Errorf("page=0: %d", rec.Code)
		}
	})

	t.Run("amministratore_dell_installazione_come_bersaglio", func(t *testing.T) {
		l := decodeAccess(t, do("carol", "/users/alice/access?perPage=100"))
		if l.Total != 5 { // tutti i repo non eliminati
			t.Fatalf("total = %d", l.Total)
		}
		for _, it := range l.Items {
			if it.Role != openapi.ResourceRoleAdmin || len(it.Sources) != 1 || it.Sources[0].Kind != openapi.AccessSourceKindInstallationAdmin {
				t.Errorf("%s: %+v", it.FullName, it)
			}
		}
	})

	t.Run("utente_inesistente_o_organizzazione", func(t *testing.T) {
		for _, p := range []string{"/users/nobody/access", "/users/acme/access"} {
			if rec := do("carol", p); rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d", p, rec.Code)
			}
		}
	})

	t.Run("senza_identita_503", func(t *testing.T) {
		bare := httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret, httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id))
		e.router = bare
		if rec := e.do(http.MethodGet, "/users/bob/access", "carol", ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%d", rec.Code)
		}
	})
}
