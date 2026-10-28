package apps

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-458 (v3-c): normalizzazione dei domini, controllo del DNS, grazia e rotta degli host propri.

type stubDNS struct {
	cname map[string]string
	hosts map[string][]string
	err   error // errore del resolver (non NXDOMAIN)
}

func (s stubDNS) LookupCNAME(_ context.Context, h string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if c, ok := s.cname[h]; ok {
		return c + ".", nil
	}
	if _, ok := s.hosts[h]; ok {
		return h + ".", nil
	}
	return "", &net.DNSError{Name: h, IsNotFound: true}
}

func (s stubDNS) LookupHost(_ context.Context, h string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if c, ok := s.cname[h]; ok {
		h = c // il resolver segue la catena dei CNAME
	}
	if ips, ok := s.hosts[h]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Name: h, IsNotFound: true}
}

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{"Shop.Retail.IT": "shop.retail.it", " retail.it. ": "retail.it", "a-b.c1.example.org": "a-b.c1.example.org"}
	for in, want := range ok {
		if got, err := NormalizeDomain(in, "git.example.com", "apps.example.com"); err != nil || got != want {
			t.Errorf("%q → %q, %v (voluto %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"git.example.com", "x.git.example.com", "apps.example.com", "a.b.apps.example.com", "10.1.2.3", "2001:db8::1", "[::1]"} {
		if _, err := NormalizeDomain(in, "git.example.com", "apps.example.com"); !errors.Is(err, ErrDomainReserved) {
			t.Errorf("%q deve essere riservato: %v", in, err)
		}
	}
	for _, in := range []string{"", "retail", "*.retail.it", "https://a.it", "a.it:80", "a.it/x", "a..it", "-a.it", "a-.it", "a_b.it", "a.123", strings.Repeat("a", 64) + ".it", strings.Repeat("a.", 130) + "it", "è.it"} {
		if _, err := NormalizeDomain(in, "", ""); !errors.Is(err, ErrDomainInvalid) {
			t.Errorf("%q deve essere non valido: %v", in, err)
		}
	}
	// Un nome che finisce come il dominio riservato ma non è un sotto-dominio è ammesso.
	if _, err := NormalizeDomain("notapps.example.com", "git.example.com", "apps.example.com"); err != nil {
		t.Errorf("notapps.example.com non è sotto apps.example.com: %v", err)
	}
}

func TestCheckDNS(t *testing.T) {
	label := "web-handbook-alice"
	target := "web-handbook-alice.apps.example.com"
	ctx := context.Background()
	check := func(dns stubDNS, ips ...string) DNSResult {
		return DomainConfig{AppsDomain: "apps.example.com", Resolver: dns}.CheckDNS(ctx, "shop.retail.it", &label, ips)
	}
	if r := check(stubDNS{cname: map[string]string{"shop.retail.it": target}}); r.Status != store.DomainVerified {
		t.Errorf("CNAME giusto: %+v", r)
	}
	if r := check(stubDNS{cname: map[string]string{"shop.retail.it": "WEB-handbook-alice.apps.example.com"}}); r.Status != store.DomainVerified {
		t.Errorf("il confronto ignora le maiuscole: %+v", r)
	}
	if r := check(stubDNS{}); r.Status != store.DomainPendingDNS || r.Reason != "" || r.ResolverErr != nil {
		t.Errorf("NXDOMAIN = pending: %+v", r)
	}
	if r := check(stubDNS{err: &net.DNSError{Name: "x", IsTimeout: true}}); r.Status != store.DomainPendingDNS || r.ResolverErr == nil {
		t.Errorf("timeout = errore resolver: %+v", r)
	}
	if r := check(stubDNS{err: &net.DNSError{Name: "x", IsTemporary: true}}); r.Status != store.DomainPendingDNS || r.ResolverErr == nil {
		t.Errorf("SERVFAIL = errore resolver: %+v", r)
	}
	r := check(stubDNS{hosts: map[string][]string{"shop.retail.it": {"203.0.113.9"}}}, "198.51.100.7")
	if r.Status != store.DomainError || !strings.Contains(r.Reason, "203.0.113.9") || !strings.Contains(r.Reason, target) || !strings.Contains(r.Reason, "198.51.100.7") {
		t.Errorf("IP diverso: %+v", r)
	}
	r = check(stubDNS{cname: map[string]string{"shop.retail.it": "altro.example.net"}, hosts: map[string][]string{"altro.example.net": {"203.0.113.9"}}})
	if r.Status != store.DomainError || !strings.Contains(r.Reason, "altro.example.net") {
		t.Errorf("CNAME altrove: %+v", r)
	}
	if r := check(stubDNS{hosts: map[string][]string{"shop.retail.it": {"198.51.100.7"}}}, "198.51.100.7", "2001:db8::1"); r.Status != store.DomainVerified {
		t.Errorf("A contenuto nei dichiarati: %+v", r)
	}
	if r := check(stubDNS{hosts: map[string][]string{"shop.retail.it": {"198.51.100.7", "203.0.113.1"}}}, "198.51.100.7"); r.Status != store.DomainError {
		t.Errorf("un IP in più non vale: %+v", r)
	}
	// Senza IP dichiarati la forma A non è proposta: un A qualunque è errore.
	if r := check(stubDNS{hosts: map[string][]string{"shop.retail.it": {"198.51.100.7"}}}); r.Status != store.DomainError {
		t.Errorf("senza IP dichiarati: %+v", r)
	}
	// Un indirizzo interno è segnalato come tale.
	r = check(stubDNS{hosts: map[string][]string{"shop.retail.it": {"127.0.0.1"}}})
	if r.Status != store.DomainError || !strings.Contains(r.Reason, "riservato o interno") {
		t.Errorf("loopback: %+v", r)
	}
}

func TestExpectedRecord(t *testing.T) {
	label := "web-handbook-alice"
	c := DomainConfig{AppsDomain: "apps.example.com"}
	if r := c.ExpectedRecord("shop.retail.it", &label, nil); r.Type != "CNAME" || r.Value != "web-handbook-alice.apps.example.com" || len(r.Alternatives) != 0 {
		t.Errorf("sotto-dominio: %+v", r)
	}
	r := c.ExpectedRecord("shop.retail.it", &label, []string{"198.51.100.7", "2001:db8::7"})
	if r.Type != "CNAME" || len(r.Alternatives) != 2 {
		t.Errorf("sotto-dominio con IP: %+v", r)
	}
	r = c.ExpectedRecord("retail.it", &label, []string{"198.51.100.7", "2001:db8::7"})
	if r.Type != "A" || r.Value != "198.51.100.7" || len(r.Alternatives) != 1 || r.Alternatives[0].Type != "AAAA" {
		t.Errorf("apice: %+v", r)
	}
}

func TestApplyDNSResult_Grazia(t *testing.T) {
	c := DomainConfig{Grace: time.Hour}
	t0 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	d := store.AppDomain{Normalized: "shop.retail.it", Status: store.DomainPendingDNS}
	v := store.AppDomain{Normalized: "shop.retail.it", Status: store.DomainVerified, VerifiedAt: &t0}
	for _, resolverErr := range []error{
		&net.DNSError{Name: "x", IsTimeout: true},
		&net.DNSError{Name: "x", IsTemporary: true},
	} {
		chk := c.ApplyDNSResult(v, DNSResult{Status: store.DomainPendingDNS, ResolverErr: resolverErr}, t0.Add(10*time.Minute))
		if chk.Status != store.DomainVerified || chk.FailingSince != nil || chk.VerifiedAt == nil || !chk.VerifiedAt.Equal(t0) {
			t.Errorf("verified senza grazia con errore resolver %v: %+v", resolverErr, chk)
		}
	}
	checkAt := t0.Add(10 * time.Minute)
	bad := DNSResult{Status: store.DomainError, Reason: "risolve su 203.0.113.9"}
	nxdomain := (DomainConfig{AppsDomain: "apps.example.com", Resolver: stubDNS{}}).CheckDNS(context.Background(), "shop.retail.it", nil, nil)
	for name, result := range map[string]DNSResult{"NXDOMAIN": nxdomain, "record diversi": bad} {
		chk := c.ApplyDNSResult(v, result, checkAt)
		if chk.Status != store.DomainVerified || chk.FailingSince == nil || !chk.FailingSince.Equal(checkAt) {
			t.Errorf("%s deve iniziare la grazia: %+v", name, chk)
		}
	}

	// pending → verified: verified_at nasce.
	chk := c.ApplyDNSResult(d, DNSResult{Status: store.DomainVerified}, t0)
	if chk.Status != store.DomainVerified || chk.VerifiedAt == nil || !chk.VerifiedAt.Equal(t0) || chk.FailingSince != nil {
		t.Fatalf("verified: %+v", chk)
	}
	d.Status, d.VerifiedAt = chk.Status, chk.VerifiedAt

	// Il DNS non coincide più: resta verified, parte la grazia.
	chk = c.ApplyDNSResult(d, bad, t0.Add(10*time.Minute))
	if chk.Status != store.DomainVerified || chk.FailingSince == nil || !chk.FailingSince.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("grazia: %+v", chk)
	}
	d.FailingSince = chk.FailingSince
	// Dentro la grazia resta, anche se il DNS sparisce (pending).
	chk = c.ApplyDNSResult(d, DNSResult{Status: store.DomainPendingDNS}, t0.Add(40*time.Minute))
	if chk.Status != store.DomainVerified || !chk.FailingSince.Equal(*d.FailingSince) {
		t.Fatalf("ancora in grazia: %+v", chk)
	}
	resolverFailure := DNSResult{Status: store.DomainPendingDNS, ResolverErr: errors.New("SERVFAIL")}
	chk = c.ApplyDNSResult(d, resolverFailure, t0.Add(2*time.Hour))
	if chk.Status != store.DomainVerified || chk.FailingSince == nil || !chk.FailingSince.Equal(*d.FailingSince) {
		t.Fatalf("errore resolver durante grazia: %+v", chk)
	}
	// Scaduta: errore con motivo; con «pending» il motivo è generico ma leggibile.
	chk = c.ApplyDNSResult(d, bad, t0.Add(80*time.Minute))
	if chk.Status != store.DomainError || chk.StatusReason == nil || !strings.Contains(*chk.StatusReason, "203.0.113.9") {
		t.Fatalf("errore dopo la grazia: %+v", chk)
	}
	chk = c.ApplyDNSResult(d, DNSResult{Status: store.DomainPendingDNS}, t0.Add(80*time.Minute))
	if chk.Status != store.DomainError || chk.StatusReason == nil || !strings.Contains(*chk.StatusReason, "shop.retail.it") {
		t.Fatalf("errore generico: %+v", chk)
	}
	// Un esito positivo azzera la grazia.
	chk = c.ApplyDNSResult(d, DNSResult{Status: store.DomainVerified}, t0.Add(20*time.Minute))
	if chk.Status != store.DomainVerified || chk.FailingSince != nil {
		t.Fatalf("ripresa: %+v", chk)
	}
	// Un pending che trova un record sbagliato va subito in errore (mai stato verified).
	p := store.AppDomain{Normalized: "x.retail.it", Status: store.DomainPendingDNS}
	if chk := c.ApplyDNSResult(p, bad, t0); chk.Status != store.DomainError || chk.StatusReason == nil || chk.FailingSince != nil {
		t.Fatalf("pending → error: %+v", chk)
	}
}

func TestDomainCheckDue(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *time.Time { t := now.Add(-ago); return &t }
	cases := []struct {
		name string
		d    store.AppDomain
		want bool
	}{
		{"mai controllato", store.AppDomain{CreatedAt: now}, true},
		{"pending nuovo, 2 min", store.AppDomain{Status: store.DomainPendingDNS, CreatedAt: now.Add(-10 * time.Minute), LastChecked: at(2 * time.Minute)}, false},
		{"pending nuovo, 6 min", store.AppDomain{Status: store.DomainPendingDNS, CreatedAt: now.Add(-10 * time.Minute), LastChecked: at(6 * time.Minute)}, true},
		{"pending di 3 ore, 10 min", store.AppDomain{Status: store.DomainPendingDNS, CreatedAt: now.Add(-3 * time.Hour), LastChecked: at(10 * time.Minute)}, false},
		{"pending di 3 ore, 31 min", store.AppDomain{Status: store.DomainPendingDNS, CreatedAt: now.Add(-3 * time.Hour), LastChecked: at(31 * time.Minute)}, true},
		{"pending di 3 giorni, 1 ora", store.AppDomain{Status: store.DomainError, CreatedAt: now.Add(-72 * time.Hour), LastChecked: at(time.Hour)}, false},
		{"pending di 3 giorni, 7 ore", store.AppDomain{Status: store.DomainError, CreatedAt: now.Add(-72 * time.Hour), LastChecked: at(7 * time.Hour)}, true},
		{"verified, 5 min", store.AppDomain{Status: store.DomainVerified, CreatedAt: now.Add(-72 * time.Hour), LastChecked: at(5 * time.Minute)}, false},
		{"verified, 11 min", store.AppDomain{Status: store.DomainVerified, CreatedAt: now.Add(-72 * time.Hour), LastChecked: at(11 * time.Minute)}, true},
	}
	for _, c := range cases {
		if got := DomainCheckDue(c.d, now); got != c.want {
			t.Errorf("%s: %v, voluto %v", c.name, got, c.want)
		}
	}
	grace := store.AppDomain{Status: store.DomainVerified, CreatedAt: now.Add(-72 * time.Hour), LastChecked: at(6 * time.Minute), FailingSince: at(time.Minute)}
	if !DomainCheckDue(grace, now) {
		t.Error("in grazia si ricontrolla ogni 5 minuti")
	}
}

// Il dominio attivo aggiunge il suo host alla rotta; tolto, la rotta torna al solo indirizzo automatico.
func TestEnsureRoute_HostDeiDominiPropri(t *testing.T) {
	ctx := context.Background()
	app, ver := testApp()
	app.Access = "public"
	dyn := fakeDynamic()
	domains := []string{"shop.retail.it"}
	c := &Controller{dyn: dyn, cfg: Config{AppsDomain: "apps.example.com"},
		publicOn:  func(context.Context) (bool, error) { return true, nil },
		domainsOf: func(context.Context, store.App) ([]string, error) { return domains, nil }}
	ns := Namespace(app.ID)
	match := func() string {
		t.Helper()
		u, err := dyn.Resource(routeGVR).Namespace(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
		return routes[0].(map[string]any)["match"].(string)
	}
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	auto := "Host(`" + *app.AddressLabel + ".apps.example.com`)"
	if got := match(); got != auto+" || Host(`shop.retail.it`)" {
		t.Fatalf("rotta con dominio: %s", got)
	}
	domains = nil
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if got := match(); got != auto {
		t.Fatalf("dominio tolto, la rotta torna all'indirizzo automatico: %s", got)
	}
	// Un'app non più pubblica non porta i domini nella rotta, anche se ne ha.
	domains = []string{"shop.retail.it"}
	app.Access = "none"
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if got := match(); got != auto {
		t.Fatalf("app non pubblica: %s", got)
	}
	// Interruttore spento: nessuna rotta, nemmeno per i domini.
	app.Access = "public"
	c.publicOn = func(context.Context) (bool, error) { return false, nil }
	if err := c.ensureRoute(ctx, app, ver.Config); err != nil {
		t.Fatal(err)
	}
	if got := match(); got != "" {
		t.Fatalf("interruttore spento: nessuna rotta, trovata %s", got)
	}
}
