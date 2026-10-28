//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-473 (v3-c): elenco admin delle app pubbliche e App.publicAddress.

func (e *appsEnv) publicApps(user string) openapi.AdminPublicAppList {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/admin/apps/public", user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.AdminPublicAppList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatalf("elenco non valido: %v: %s", err, rec.Body.String())
	}
	return l
}

func (e *appsEnv) publicAddressOf(user string) *string {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/handbook/apps/web", user, "")
	e.want(rec, http.StatusOK, "")
	var a openapi.App
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		e.t.Fatal(err)
	}
	return a.PublicAddress
}

func TestAdminPublicApps_SoloAdminEPublicAddress(t *testing.T) {
	dns := newFakeDNS()
	e := newAppsEnv(t, httpserver.WithAppController(&routeCtl{}), httpserver.WithAppDomains(httpserver.AppDomainsConfig{
		InstanceHost: "git.example.com", Domains: apps.DomainConfig{Resolver: dns, Grace: 1}}))
	e.id.sysAdmin[users["carol"]] = true
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web/domains"
	auto := "https://web-handbook-alice.apps.example.com"

	// Solo l'amministratore dell'installazione: né l'owner né chi ha write.
	e.want(e.do(http.MethodGet, "/admin/apps/public", "alice", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, "/admin/apps/public", "bob", ""), http.StatusForbidden, "forbidden")

	// Interruttore spento e app interna: elenco vuoto, publicAddress = indirizzo automatico.
	if l := e.publicApps("carol"); len(l.Items) != 0 || l.Total != 0 {
		t.Fatalf("senza app pubbliche l'elenco è vuoto: %+v", l)
	}
	if p := e.publicAddressOf("bob"); p == nil || *p != auto {
		t.Fatalf("publicAddress automatico: %v", p)
	}

	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")

	// Pubblica, senza domini: una riga con l'indirizzo automatico e nessun dominio.
	l := e.publicApps("carol")
	if len(l.Items) != 1 || l.Total != 1 {
		t.Fatalf("una app pubblica: %+v", l)
	}
	row := l.Items[0]
	if row.Owner != "alice" || row.Repo != "handbook" || row.Name != "web" || len(row.Domains) != 0 || row.PublicAddress == nil || *row.PublicAddress != auto {
		t.Fatalf("riga: %+v", row)
	}

	// Dominio in attesa del DNS: non è attivo, publicAddress resta l'automatico, ma compare con il certificato.
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusCreated, "")
	l = e.publicApps("carol")
	if len(l.Items[0].Domains) != 1 || l.Items[0].Domains[0].Status != openapi.AppDomainStatusPendingDns || l.Items[0].Domains[0].Certificate.State != "none" {
		t.Fatalf("dominio pending: %+v", l.Items[0].Domains)
	}
	if p := e.publicAddressOf("bob"); p == nil || *p != auto {
		t.Fatalf("pending: publicAddress resta l'automatico: %v", p)
	}

	// Dominio verificato: publicAddress è il dominio, anche per chi ha solo write (listAppDomains gli dà 403).
	dns.set("shop.retail.it", "web-handbook-alice.apps.example.com")
	e.want(e.do(http.MethodPost, base+"/shop.retail.it/recheck", "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodGet, base, "bob", ""), http.StatusForbidden, "forbidden")
	if p := e.publicAddressOf("bob"); p == nil || *p != "https://shop.retail.it" {
		t.Fatalf("verified: publicAddress è il dominio: %v", p)
	}
	rec := e.do(http.MethodGet, "/repos/alice/handbook/apps", "bob", "")
	e.want(rec, http.StatusOK, "")
	var al openapi.AppList
	if err := json.Unmarshal(rec.Body.Bytes(), &al); err != nil || len(al.Items) != 1 || al.Items[0].PublicAddress == nil || *al.Items[0].PublicAddress != "https://shop.retail.it" {
		t.Fatalf("la lista delle app porta publicAddress: %v %s", err, rec.Body.String())
	}
	l = e.publicApps("carol")
	if r := l.Items[0]; r.PublicAddress == nil || *r.PublicAddress != "https://shop.retail.it" || r.Domains[0].Status != openapi.AppDomainStatusVerified || r.Address == nil || *r.Address != auto {
		t.Fatalf("riga verificata: %+v", r)
	}

	// Interruttore spento: le app non sono esposte, elenco vuoto e publicAddress torna l'automatico.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(false)), http.StatusOK, "")
	if l := e.publicApps("carol"); len(l.Items) != 0 {
		t.Fatalf("interruttore spento: %+v", l)
	}
	if p := e.publicAddressOf("bob"); p == nil || *p != auto {
		t.Fatalf("spento: publicAddress automatico: %v", p)
	}
}
