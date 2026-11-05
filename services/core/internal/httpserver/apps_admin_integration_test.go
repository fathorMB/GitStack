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

// GIT-547: GET /admin/apps e GET/PUT /repos/{owner}/{repo}/apps/{app}/resources non sono più 501.

func (e *appsEnv) adminApps(path, user string) openapi.AdminAppList {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.AdminAppList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatalf("elenco non valido: %v: %s", err, rec.Body.String())
	}
	return l
}

func TestAdminApps_ElencoPermessiEPaginazione(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(&routeCtl{}))
	e.id.sysAdmin[users["carol"]] = true

	// 401 senza login, 403 per chi non amministra l'istanza (anche owner del repo).
	anon := httptest.NewRecorder()
	e.router.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/admin/apps", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("senza login: %d %s", anon.Code, anon.Body.String())
	}
	e.want(e.do(http.MethodGet, "/admin/apps", "alice", ""), http.StatusForbidden, "forbidden")

	// Zero app: 200 con elenco vuoto (non null).
	rec := e.do(http.MethodGet, "/admin/apps", "carol", "")
	e.want(rec, http.StatusOK, "")
	if got := rec.Body.String(); got != "{\"items\":[],\"total\":0}\n" && got != "{\"items\":[],\"total\":0}" {
		t.Fatalf("elenco vuoto: %q", got)
	}

	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"api","image":"sha-1","port":8080,"copies":2,"resources":{"guaranteed":{"cpuMillis":100,"memoryBytes":134217728},"limit":{"cpuMillis":200,"memoryBytes":268435456}}}`), http.StatusAccepted, "")

	l := e.adminApps("/admin/apps", "carol")
	if l.Total != 2 || len(l.Items) != 2 {
		t.Fatalf("due app: %+v", l)
	}
	row := l.Items[0] // ordinate per owner, repo, nome: api prima di web
	if row.Owner != "alice" || row.Repo != "handbook" || row.Name != "api" || row.Copies != 2 ||
		row.Guaranteed.CpuMillis != 100 || row.Limit.CpuMillis != 200 || row.Limit.MemoryBytes != 268435456 || row.AboveGuaranteed {
		t.Fatalf("riga api: %+v", row)
	}
	if row.Usage.CpuMillis != 0 || row.Usage.MemoryBytes != 0 {
		t.Fatalf("senza campioni l'uso è zero: %+v", row.Usage)
	}

	// Paginazione: il totale resta quello dell'istanza.
	p := e.adminApps("/admin/apps?page=2&perPage=1", "carol")
	if p.Total != 2 || len(p.Items) != 1 || p.Items[0].Name != "web" {
		t.Fatalf("pagina 2: %+v", p)
	}
	e.want(e.do(http.MethodGet, "/admin/apps?perPage=0", "carol", ""), http.StatusBadRequest, "invalid_per_page")
}

func TestAppResources_LeggeEImposta(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(&routeCtl{}))
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	path := "/repos/alice/handbook/apps/web/resources"

	rec := e.do(http.MethodGet, path, "bob", "")
	e.want(rec, http.StatusOK, "")
	var r openapi.AppResources
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.MaxPerApp == nil || r.MaxPerApp.CpuMillis < 1 {
		t.Fatalf("manca il massimo per app: %+v", r)
	}

	body := `{"guaranteed":{"cpuMillis":250,"memoryBytes":134217728},"limit":{"cpuMillis":500,"memoryBytes":268435456}}`
	// write non basta (serve admin); il tetto sotto il garantito è 422; oltre il massimo è 422 limit_too_high.
	e.want(e.do(http.MethodPut, path, "bob", body), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, path, "alice", `{"guaranteed":{"cpuMillis":500,"memoryBytes":100},"limit":{"cpuMillis":100,"memoryBytes":100}}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPut, path, "alice", `{"guaranteed":{"cpuMillis":250,"memoryBytes":134217728},"limit":{"cpuMillis":999000,"memoryBytes":268435456}}`), http.StatusUnprocessableEntity, "limit_too_high")

	rec = e.do(http.MethodPut, path, "alice", body)
	e.want(rec, http.StatusOK, "")
	rec = e.do(http.MethodGet, path, "bob", "")
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Guaranteed.CpuMillis != 250 || r.Limit.CpuMillis != 500 || r.Limit.MemoryBytes != 268435456 {
		t.Fatalf("risorse non salvate: %+v", r)
	}
	// La modifica è una nuova versione di configurazione (H8).
	if v := e.versions("/repos/alice/handbook/apps/web/versions", "alice"); len(v.Items) != 2 {
		t.Fatalf("attesa una versione in più: %+v", v)
	}
	// Un'app che non c'è: 404.
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/nope/resources", "alice", body), http.StatusNotFound, "")
}
