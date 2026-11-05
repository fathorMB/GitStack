//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

func (e *appsEnv) userApps(path, user string) openapi.UserAppList {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.UserAppList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatalf("elenco non valido: %v: %s", err, rec.Body.String())
	}
	return l
}

// GIT-548: GET /user/apps elenca solo le app dei repo che il chiamante può leggere.
func TestUserApps_SoloRepoLeggibili(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(&routeCtl{}))
	e.id.sysAdmin[users["carol"]] = true

	anon := httptest.NewRecorder()
	e.router.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/user/apps", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("senza login: %d %s", anon.Code, anon.Body.String())
	}

	e.repo("segreto", false) // privato di alice
	e.repo("handbook", true) // interno
	e.want(e.do(http.MethodPost, "/repos/alice/segreto/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")

	// bob non ha ruoli su «segreto»: vede solo l'app del repo interno.
	l := e.userApps("/user/apps", "bob")
	if !l.HostingEnabled || l.Total != 1 || len(l.Items) != 1 || l.Items[0].Repo != "handbook" || l.Items[0].Owner != "alice" || l.Items[0].Name != "web" {
		t.Fatalf("bob: %+v", l)
	}
	// alice vede entrambe, carol (admin di sistema) pure.
	for _, u := range []string{"alice", "carol"} {
		l = e.userApps("/user/apps", u)
		if l.Total != 2 || len(l.Items) != 2 || l.Items[0].Repo != "handbook" || l.Items[1].Repo != "segreto" {
			t.Fatalf("%s: %+v", u, l)
		}
	}
	// Paginazione: il totale resta quello dei repo leggibili.
	l = e.userApps("/user/apps?page=2&perPage=1", "alice")
	if l.Total != 2 || len(l.Items) != 1 || l.Items[0].Repo != "segreto" || l.Page != 2 || l.PerPage != 1 {
		t.Fatalf("pagina 2: %+v", l)
	}
	e.want(e.do(http.MethodGet, "/user/apps?perPage=0", "alice", ""), http.StatusBadRequest, "invalid_per_page")
}

// GIT-548: con l'hosting spento l'elenco è 200 vuoto con hostingEnabled=false, non 503.
func TestUserApps_HostingSpento(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppsHostingOff())
	e.repo("handbook", true)
	l := e.userApps("/user/apps", "alice")
	if l.HostingEnabled || len(l.Items) != 0 {
		t.Fatalf("hosting spento: %+v", l)
	}
}
