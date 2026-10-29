package apps

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func testLeaf(t *testing.T, names []string, from, until time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "x"},
		NotBefore: from, NotAfter: until, DNSNames: names}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestBuildDomainsRoute_UnaRegolaPerDominioConResolver (GIT-459): la rotta dei domini propri cita il
// resolver ACME condiviso, ha una regola per dominio e gli stessi middleware della rotta principale;
// la rotta dell'indirizzo automatico non cita mai il resolver.
func TestBuildDomainsRoute_UnaRegolaPerDominioConResolver(t *testing.T) {
	app, _ := testApp()
	r := BuildDomainsRoute(app, []string{"a.example.org", "b.example.org"}, true, "letsencrypt")
	if r.GetName() != DomainsRouteName {
		t.Fatalf("nome: %s", r.GetName())
	}
	if res, _, _ := unstructured.NestedString(r.Object, "spec", "tls", "certResolver"); res != "letsencrypt" {
		t.Fatalf("certResolver: %q", res)
	}
	routes, _, _ := unstructured.NestedSlice(r.Object, "spec", "routes")
	if len(routes) != 2 {
		t.Fatalf("una regola per dominio: %d", len(routes))
	}
	if m := routes[1].(map[string]any)["match"]; m != "Host(`b.example.org`)" {
		t.Fatalf("match: %v", m)
	}
	if mws := routes[0].(map[string]any)["middlewares"].([]any); len(mws) != 2 {
		t.Fatalf("strip e auth attesi: %v", mws)
	}
	main := BuildRoute(app, "x.apps.example.com", false)
	if _, found, _ := unstructured.NestedString(main.Object, "spec", "tls", "certResolver"); found {
		t.Fatal("la rotta dell'indirizzo automatico non deve chiedere un certificato pubblico")
	}
}

// TestEnsureRoute_DominiLetsEncryptInRottaPropria (GIT-459): con il resolver configurato i domini
// verified vanno nella rotta -domains; senza domini (o a interruttore spento) la rotta sparisce.
func TestEnsureRoute_DominiLetsEncryptInRottaPropria(t *testing.T) {
	ctx := context.Background()
	app, ver := testApp()
	app.RepoID = uuid.New()
	app.Access = "public"
	dyn := fakeDynamic()
	on := true
	domains := []string{"shop.example.org"}
	c := &Controller{dyn: dyn, cfg: Config{AppsDomain: "apps.example.com", ACME: ACMEConfig{Resolver: "letsencrypt"}},
		publicOn:  func(context.Context) (bool, error) { return on, nil },
		domainsOf: func(context.Context, store.App) ([]string, error) { return domains, nil }}
	get := func(name string) *unstructured.Unstructured {
		r, err := dyn.Resource(routeGVR).Namespace(Namespace(app.ID)).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if main := get(WorkloadName); main == nil {
		t.Fatal("manca la rotta principale")
	} else if m, _, _ := unstructured.NestedSlice(main.Object, "spec", "routes"); m[0].(map[string]any)["match"] == "" || len(m) != 1 {
		t.Fatal("rotta principale inattesa")
	}
	d := get(DomainsRouteName)
	if d == nil {
		t.Fatal("manca la rotta dei domini")
	}
	if res, _, _ := unstructured.NestedString(d.Object, "spec", "tls", "certResolver"); res != "letsencrypt" {
		t.Fatalf("certResolver: %q", res)
	}
	domains = nil
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if get(DomainsRouteName) != nil {
		t.Fatal("senza domini la rotta dei domini deve sparire")
	}
	domains = []string{"shop.example.org"}
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	on = false
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if get(WorkloadName) != nil || get(DomainsRouteName) != nil {
		t.Fatal("interruttore spento: nessuna delle due rotte")
	}
}

// Senza resolver i domini restano nella rotta principale, come prima di GIT-459.
func TestEnsureRoute_SenzaResolverDominiNellaRottaPrincipale(t *testing.T) {
	ctx := context.Background()
	app, ver := testApp()
	app.RepoID = uuid.New()
	app.Access = "public"
	dyn := fakeDynamic()
	c := &Controller{dyn: dyn, cfg: Config{AppsDomain: "apps.example.com"},
		publicOn:  func(context.Context) (bool, error) { return true, nil },
		domainsOf: func(context.Context, store.App) ([]string, error) { return []string{"shop.example.org"}, nil }}
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(routeGVR).Namespace(Namespace(app.ID)).Get(ctx, DomainsRouteName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("senza resolver non ci deve essere la rotta dei domini: %v", err)
	}
}

func TestCertificateFromServed(t *testing.T) {
	now := time.Now()
	good := testLeaf(t, []string{"shop.example.org"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	if ok, why := CertificateFromServed(good, "shop.example.org", now); !ok {
		t.Fatalf("valido atteso: %s", why)
	}
	if ok, _ := CertificateFromServed(good, "altro.example.org", now); ok {
		t.Fatal("un certificato che non copre il dominio (default di Traefik, wildcard delle app) non vale")
	}
	old := testLeaf(t, []string{"shop.example.org"}, now.Add(-48*time.Hour), now.Add(-time.Hour))
	if ok, _ := CertificateFromServed(old, "shop.example.org", now); ok {
		t.Fatal("scaduto non vale")
	}
	if ok, _ := CertificateFromServed(nil, "x.example.org", now); ok {
		t.Fatal("nil non vale")
	}
}

func TestCertOutcome_IssuingValidError(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	d := store.AppDomain{ID: uuid.New(), Normalized: "shop.example.org", Status: store.DomainVerified, CertMode: "letsencrypt", CertState: "none"}
	var served *x509.Certificate
	var probeErr error
	c := &Controller{cfg: Config{ACME: ACMEConfig{Resolver: "letsencrypt", TLSAddr: "t:443",
		Probe: func(context.Context, string, string) (*x509.Certificate, error) { return served, probeErr }}}}
	c.acme.issuing = map[uuid.UUID]time.Time{}

	// Traefik serve ancora il certificato di default: issuing.
	served = testLeaf(t, []string{"*.apps.example.com"}, now.Add(-time.Hour), now.Add(time.Hour))
	got, ok := c.certOutcome(ctx, d, now)
	if !ok || got.State != "issuing" {
		t.Fatalf("issuing atteso: %+v", got)
	}
	// Oltre il tempo massimo: error con il motivo.
	got, _ = c.certOutcome(ctx, d, now.Add(acmeIssueTimeout+time.Minute))
	if got.State != "error" || got.Error == nil || *got.Error == "" {
		t.Fatalf("error atteso: %+v", got)
	}
	// Arriva il certificato: valid con scadenza ed emittente, e il contatore riparte.
	served = testLeaf(t, []string{"shop.example.org"}, now.Add(-time.Minute), now.Add(90*24*time.Hour))
	got, _ = c.certOutcome(ctx, d, now.Add(acmeIssueTimeout+2*time.Minute))
	if got.State != "valid" || got.ValidUntil == nil || got.Issuer == nil || *got.Issuer != "x" {
		t.Fatalf("valid atteso: %+v", got)
	}
	if _, left := c.acme.issuing[d.ID]; left {
		t.Fatal("il dominio valid non deve restare in attesa")
	}
	// Un errore di rete su un dominio già valid non lo riporta a issuing.
	d.CertState = "valid"
	served, probeErr = nil, errors.New("connessione rifiutata")
	if _, ok := c.certOutcome(ctx, d, now); ok {
		t.Fatal("un errore di rete non deve cambiare un certificato valid")
	}
	// Dominio non più verified: none.
	d.Status = store.DomainError
	if got, ok := c.certOutcome(ctx, d, now); !ok || got.State != "none" {
		t.Fatalf("none atteso: %+v", got)
	}
}

func TestProbePort80(t *testing.T) {
	ctx := context.Background()
	c := &Controller{cfg: Config{Now: time.Now, ACME: ACMEConfig{Resolver: "letsencrypt", Port80: 8080,
		DialPort80: func(_ context.Context, addr string) error {
			if addr == "203.0.113.9:8080" {
				return errors.New("timeout")
			}
			return nil
		}}}}
	if st := c.probePort80(ctx, nil); st.Reachable != nil {
		t.Fatal("senza IP dichiarati: non verificato")
	}
	if st := c.probePort80(ctx, []string{"198.51.100.4"}); st.Reachable == nil || !*st.Reachable || st.Error != "" {
		t.Fatalf("raggiungibile atteso: %+v", st)
	}
	st := c.probePort80(ctx, []string{"198.51.100.4", "203.0.113.9"})
	if st.Reachable == nil || *st.Reachable || st.Error == "" || st.CheckedAt == nil {
		t.Fatalf("non raggiungibile atteso: %+v", st)
	}
}
