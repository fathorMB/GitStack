//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-458 (v3-c): domini propri con controllo del DNS.

// fakeDNS è il resolver dei test: record per nome, NXDOMAIN per gli altri.
type fakeDNS struct {
	mu     sync.Mutex
	cname  map[string]string
	a      map[string][]string
	lookup int
	err    error
}

func newFakeDNS() *fakeDNS { return &fakeDNS{cname: map[string]string{}, a: map[string][]string{}} }

func (f *fakeDNS) set(host, cname string, ips ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cname, host)
	delete(f.a, host)
	if cname != "" {
		f.cname[host] = cname
	}
	if len(ips) > 0 {
		f.a[host] = ips
	}
}

func (f *fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookup++
	if f.err != nil {
		return "", f.err
	}
	if c, ok := f.cname[host]; ok {
		return c + ".", nil
	}
	if _, ok := f.a[host]; ok {
		return host + ".", nil
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (f *fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if ips, ok := f.a[host]; ok {
		return ips, nil
	}
	if c, ok := f.cname[host]; ok {
		if ips, ok := f.a[c]; ok {
			return ips, nil
		}
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// routeCtl è il controller finto: conta i riallineamenti della rotta.
type routeCtl struct {
	mu      sync.Mutex
	refresh []store.App
}

func (*routeCtl) Kick()                                    {}
func (*routeCtl) Restart(context.Context, store.App) error { return nil }
func (*routeCtl) Scale(context.Context, store.App) error   { return nil }
func (c *routeCtl) RefreshRoute(_ context.Context, a store.App) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refresh = append(c.refresh, a)
}
func (c *routeCtl) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.refresh)
}

func (e *appsEnv) domain(rec interface{ Bytes() []byte }) openapi.AppDomain {
	e.t.Helper()
	var d openapi.AppDomain
	if err := json.Unmarshal(rec.Bytes(), &d); err != nil {
		e.t.Fatalf("dominio non valido: %v: %s", err, rec.Bytes())
	}
	return d
}

func domainBody(d string) string { return `{"domain":"` + d + `"}` }

func TestAppDomains_AggiuntaConflittoRiservatiVerificaERimozione(t *testing.T) {
	dns := newFakeDNS()
	ctl := &routeCtl{}
	e := newAppsEnv(t, httpserver.WithAppController(ctl), httpserver.WithAppDomains(httpserver.AppDomainsConfig{
		InstanceHost: "git.example.com", Domains: apps.DomainConfig{Resolver: dns, Grace: 1}}))
	e.id.sysAdmin[users["carol"]] = true
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.repo("other", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, "/repos/alice/other/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web/domains"

	// App non pubblica: nessun dominio.
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusConflict, "app_not_public")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusConflict, "app_not_public")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/other/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")

	// Solo admin dell'app: write non basta.
	e.want(e.do(http.MethodPost, base, "bob", domainBody("shop.retail.it")), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base, "bob", ""), http.StatusForbidden, "forbidden")

	// Riservati e non validi.
	for _, d := range []string{"git.example.com", "wiki.git.example.com", "apps.example.com", "x.apps.example.com", "web-handbook-alice.apps.example.com", "10.0.0.1", "::1"} {
		e.want(e.do(http.MethodPost, base, "alice", domainBody(d)), http.StatusUnprocessableEntity, "domain_reserved")
	}
	for _, d := range []string{"*.retail.it", "retail", "https://shop.retail.it", "shop.retail.it:8443", "-a.retail.it", "a_b.retail.it", ""} {
		e.want(e.do(http.MethodPost, base, "alice", domainBody(d)), http.StatusUnprocessableEntity, "invalid_domain")
	}
	if l := e.domains(base); len(l.Items) != 0 {
		t.Fatalf("i rifiuti non salvano nulla: %d", len(l.Items))
	}

	// Aggiunta: il DNS non risponde ancora → pending_dns, con il record atteso (CNAME verso l'indirizzo automatico).
	rec := e.do(http.MethodPost, base, "alice", domainBody("Shop.Retail.IT."))
	e.want(rec, http.StatusCreated, "")
	d := e.domain(rec.Body)
	if d.Domain != "shop.retail.it" || d.Status != openapi.AppDomainStatusPendingDns || d.StatusReason != nil {
		t.Fatalf("pending: %+v", d)
	}
	if d.Dns.Type != "CNAME" || d.Dns.Value != "web-handbook-alice.apps.example.com" || d.Dns.Name != "shop.retail.it" {
		t.Fatalf("record atteso: %+v", d.Dns)
	}
	if d.Certificate.Mode != "letsencrypt" || d.Certificate.State != "none" || d.CreatedBy == nil || d.CreatedBy.Username != "alice" || d.LastCheckedAt == nil {
		t.Fatalf("certificato/autore/controllo: %+v", d)
	}

	// Conflitto: lo stesso dominio (anche con altro formato) è già di un'app, anche della stessa.
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusConflict, "domain_taken")
	e.want(e.do(http.MethodPost, "/repos/alice/other/apps/web/domains", "alice", domainBody("SHOP.retail.it")), http.StatusConflict, "domain_taken")

	// Verifica fallita con motivo leggibile: il dominio risponde un altro IP.
	dns.set("shop.retail.it", "", "203.0.113.9")
	rec = e.do(http.MethodPost, base+"/shop.retail.it/recheck", "alice", "")
	e.want(rec, http.StatusOK, "")
	d = e.domain(rec.Body)
	if d.Status != openapi.AppDomainStatusError || d.StatusReason == nil || !strings.Contains(*d.StatusReason, "203.0.113.9") || !strings.Contains(*d.StatusReason, "web-handbook-alice.apps.example.com") {
		t.Fatalf("errore con motivo: %+v", d)
	}
	if ctl.count() != 0 {
		t.Fatal("senza verified la rotta non cambia")
	}

	// Verifica riuscita: il CNAME arriva all'indirizzo automatico; la rotta si riallinea.
	dns.set("shop.retail.it", "web-handbook-alice.apps.example.com")
	rec = e.do(http.MethodPost, base+"/shop.retail.it/recheck", "alice", "")
	e.want(rec, http.StatusOK, "")
	d = e.domain(rec.Body)
	if d.Status != openapi.AppDomainStatusVerified || d.StatusReason != nil || d.VerifiedAt == nil {
		t.Fatalf("verified: %+v", d)
	}
	if ctl.count() != 1 {
		t.Fatalf("la rotta va riallineata all'ingresso in verified: %d", ctl.count())
	}
	vh, err := store.New(e.pool).VerifiedDomainHosts(context.Background(), ctl.refresh[0].ID)
	if err != nil || len(vh) != 1 || vh[0] != "shop.retail.it" {
		t.Fatalf("domini attivi per la rotta: %v %v", vh, err)
	}

	// Get e lista.
	e.want(e.do(http.MethodGet, base+"/shop.retail.it", "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodGet, base+"/nessuno.retail.it", "alice", ""), http.StatusNotFound, "not_found")
	if l := e.domains(base); len(l.Items) != 1 || l.Items[0].Status != openapi.AppDomainStatusVerified {
		t.Fatalf("lista: %+v", l)
	}
	if l := e.domains("/repos/alice/other/apps/web/domains"); len(l.Items) != 0 {
		t.Fatal("i domini sono per app")
	}

	// Rimozione: 204, la rotta si riscrive subito, il dominio torna libero per altre app.
	before := ctl.count()
	e.want(e.do(http.MethodDelete, base+"/shop.retail.it", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/shop.retail.it", "alice", ""), http.StatusNoContent, "")
	if ctl.count() != before+1 {
		t.Fatalf("la rimozione deve riallineare la rotta: %d → %d", before, ctl.count())
	}
	if vh, _ := store.New(e.pool).VerifiedDomainHosts(context.Background(), ctl.refresh[0].ID); len(vh) != 0 {
		t.Fatalf("senza dominio la rotta non ha host propri: %v", vh)
	}
	e.want(e.do(http.MethodDelete, base+"/shop.retail.it", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/repos/alice/other/apps/web/domains", "alice", domainBody("shop.retail.it")), http.StatusCreated, "")
}

func TestAppDomains_ApiceConIpDichiarati(t *testing.T) {
	dns := newFakeDNS()
	e := newAppsEnv(t, httpserver.WithAppDomains(httpserver.AppDomainsConfig{Domains: apps.DomainConfig{Resolver: dns}}))
	e.id.sysAdmin[users["carol"]] = true
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.app_capacity_settings SET public_ips = '{198.51.100.7,2001:db8::7}'`); err != nil {
		t.Fatal(err)
	}
	base := "/repos/alice/handbook/apps/web/domains"

	rec := e.do(http.MethodPost, base, "alice", domainBody("retail.it"))
	e.want(rec, http.StatusCreated, "")
	d := e.domain(rec.Body)
	if d.Dns.Type != "A" || d.Dns.Value != "198.51.100.7" || len(d.Dns.Alternatives) != 1 || d.Dns.Alternatives[0].Type != "AAAA" {
		t.Fatalf("all'apice A/AAAA: %+v", d.Dns)
	}
	// Un IP in più oltre a quelli dichiarati non vale; solo dichiarati vale.
	dns.set("retail.it", "", "198.51.100.7", "203.0.113.1")
	d = e.domain(e.do(http.MethodPost, base+"/retail.it/recheck", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusError {
		t.Fatalf("IP non dichiarato: %+v", d)
	}
	dns.set("retail.it", "", "198.51.100.7", "2001:db8::7")
	d = e.domain(e.do(http.MethodPost, base+"/retail.it/recheck", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusVerified {
		t.Fatalf("A/AAAA dichiarati: %+v", d)
	}
}

// Il ricontrollo periodico aggiorna lo stato; un verified che smette di puntare resta verified
// (con la rotta) durante la grazia e poi passa in errore.
func TestAppDomains_RicontrolloPeriodicoEGrazia(t *testing.T) {
	dns := newFakeDNS()
	e := newAppsEnv(t, httpserver.WithAppDomains(httpserver.AppDomainsConfig{Domains: apps.DomainConfig{Resolver: dns}}))
	e.id.sysAdmin[users["carol"]] = true
	repoID := e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	base := "/repos/alice/handbook/apps/web/domains"
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusCreated, "")

	st := store.New(e.pool)
	ctx := context.Background()
	app, err := st.GetApp(ctx, repoID, "web")
	if err != nil {
		t.Fatal(err)
	}
	ctl := apps.New(st, nil, nil, apps.Config{AppsDomain: "apps.example.com", Domains: apps.DomainConfig{Resolver: dns, AppsDomain: "apps.example.com", Grace: 90 * time.Minute}})
	// Il ritmo: appena controllato non è «due», dopo 5 minuti sì.
	ds, _ := st.ListAppDomains(ctx, app.ID)
	if apps.DomainCheckDue(ds[0], ds[0].LastChecked.Add(time.Minute)) || !apps.DomainCheckDue(ds[0], ds[0].LastChecked.Add(6*time.Minute)) {
		t.Fatal("ritmo del ricontrollo di un pending: 5 minuti")
	}

	// Il DNS compare: il ciclo (CheckDomains) lo porta a verified. Il last_checked va indietro per renderlo «due».
	dns.set("shop.retail.it", "web-handbook-alice.apps.example.com")
	age := func() {
		if _, err := e.pool.Exec(ctx, `UPDATE core.app_domains SET last_checked_at = last_checked_at - interval '2 hours'`); err != nil {
			t.Fatal(err)
		}
	}
	age()
	ctl.CheckDomains(ctx)
	d := e.domain(e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusVerified {
		t.Fatalf("il ciclo periodico deve verificare: %+v", d)
	}

	// Il DNS cambia: nella grazia resta verified e con la rotta.
	dns.set("shop.retail.it", "", "203.0.113.50")
	age()
	ctl.CheckDomains(ctx)
	d = e.domain(e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusVerified {
		t.Fatalf("nella grazia resta verified: %+v", d)
	}
	if hosts, _ := st.VerifiedDomainHosts(ctx, app.ID); len(hosts) != 1 {
		t.Fatalf("la rotta non si toglie subito: %v", hosts)
	}
	// Scaduta la grazia (failing_since indietro di 2 ore) passa in errore col motivo, senza cancellare il dominio.
	if _, err := e.pool.Exec(ctx, `UPDATE core.app_domains SET failing_since = failing_since - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	age()
	ctl.CheckDomains(ctx)
	d = e.domain(e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusError || d.StatusReason == nil || !strings.Contains(*d.StatusReason, "203.0.113.50") {
		t.Fatalf("dopo la grazia: errore con motivo: %+v", d)
	}
	if hosts, _ := st.VerifiedDomainHosts(ctx, app.ID); len(hosts) != 0 {
		t.Fatalf("in errore la rotta propria sparisce: %v", hosts)
	}
	// E si riprende da solo quando il DNS torna a posto.
	dns.set("shop.retail.it", "web-handbook-alice.apps.example.com")
	age()
	ctl.CheckDomains(ctx)
	d = e.domain(e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body)
	if d.Status != openapi.AppDomainStatusVerified || d.StatusReason != nil {
		t.Fatalf("ripresa: %+v", d)
	}
}

func (e *appsEnv) domains(path string) openapi.AppDomainList {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, "alice", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.AppDomainList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func TestAppDomains_RecheckLoggaErroreResolver(t *testing.T) {
	dns := newFakeDNS()
	e := newAppsEnv(t, httpserver.WithAppDomains(httpserver.AppDomainsConfig{
		InstanceHost: "git.example.com", Domains: apps.DomainConfig{Resolver: dns, Grace: 1}}))
	e.id.sysAdmin[users["carol"]] = true
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	base := "/repos/alice/handbook/apps/web/domains"
	e.want(e.do(http.MethodPost, base, "alice", domainBody("resolver.retail.it")), http.StatusCreated, "")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	dns.err = errors.New("resolver unavailable")
	e.want(e.do(http.MethodPost, base+"/resolver.retail.it/recheck", "alice", ""), http.StatusOK, "")
	if got := buf.String(); !strings.Contains(got, "apps: errore del resolver durante il controllo DNS") || !strings.Contains(got, "resolver.retail.it") {
		t.Fatalf("log del resolver assente o senza dominio: %q", got)
	}

	// NXDOMAIN è un esito DNS, non un errore del resolver: non deve produrre quel log.
	buf.Reset()
	dns.err = nil
	e.want(e.do(http.MethodPost, base+"/resolver.retail.it/recheck", "alice", ""), http.StatusOK, "")
	if got := buf.String(); strings.Contains(got, "apps: errore del resolver durante il controllo DNS") {
		t.Fatalf("NXDOMAIN non deve essere registrato come errore resolver: %q", got)
	}
}
