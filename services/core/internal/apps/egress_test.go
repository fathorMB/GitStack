package apps

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func ap(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

// ipRules: le regole con un solo IPBlock /32 o /128 e una porta (le aperture precise).
func ipRules(p *networkingv1.NetworkPolicy) map[string]bool {
	out := map[string]bool{}
	for _, r := range p.Spec.Egress {
		if len(r.To) != 1 || r.To[0].IPBlock == nil || len(r.Ports) != 1 || r.Ports[0].Port == nil || r.Ports[0].Port.IntValue() == 53 {
			continue
		}
		out[r.To[0].IPBlock.CIDR+":"+r.Ports[0].Port.String()] = true
	}
	return out
}

func internetRuleCount(p *networkingv1.NetworkPolicy) int {
	n := 0
	for _, r := range p.Spec.Egress {
		for _, to := range r.To {
			if to.IPBlock != nil && (to.IPBlock.CIDR == "0.0.0.0/0" || to.IPBlock.CIDR == "::/0") {
				n++
			}
		}
	}
	return n
}

// Criterio 1 (H16): l'amministratore apre db.azienda.lan:5432; l'app raggiunge quell'indirizzo su quella
// porta e nient'altro della rete privata, mentre internet resta aperto con le sue eccezioni.
func TestBuildNetworkPolicy_DestinazionePrivataAperta(t *testing.T) {
	app, _ := testApp()
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{PrivateOpenings: []netip.AddrPort{ap("10.20.0.5:5432"), ap("[fd00::7]:587"), ap("10.20.0.5:5432")}})
	got := ipRules(p)
	if len(got) != 2 || !got["10.20.0.5/32:5432"] || !got["fd00::7/128:587"] {
		t.Fatalf("regole delle aperture: %v", got)
	}
	if internetRuleCount(p) != 2 {
		t.Fatalf("internet resta aperto: %d", internetRuleCount(p))
	}
	// La rete privata resta tra le eccezioni di internet: l'apertura è una regola a parte, solo per quella porta.
	last := p.Spec.Egress
	var except []string
	for _, r := range last {
		for _, to := range r.To {
			if to.IPBlock != nil && to.IPBlock.CIDR == "0.0.0.0/0" {
				except = to.IPBlock.Except
			}
		}
	}
	found := false
	for _, e := range except {
		found = found || e == "10.0.0.0/8"
	}
	if !found {
		t.Fatalf("10.0.0.0/8 deve restare tra le eccezioni di internet: %v", except)
	}
}

// Anche se una voce vietata arrivasse fino al builder (per esempio da una riga scritta a mano nel
// database), la policy non la apre: cluster, nodo, loopback, link-local, porte di Kubernetes.
func TestBuildNetworkPolicy_IlBuilderNonApreMaiLeBarriere(t *testing.T) {
	app, _ := testApp()
	bad := []netip.AddrPort{ap("10.43.0.1:443"), ap("10.42.3.4:80"), ap("192.168.1.10:22"), ap("127.0.0.1:5432"), ap("169.254.169.254:80"), ap("10.20.0.5:6443"), ap("10.20.0.5:10250"), ap("[::1]:80")}
	opt := NetworkPolicyOptions{NodeAddresses: []string{"192.168.1.10"}}
	opt.PrivateOpenings = bad
	if got := ipRules(BuildNetworkPolicy(app, opt)); len(got) != 0 {
		t.Fatalf("nessuna barriera si apre dalle aperture: %v", got)
	}
	opt.PrivateOpenings = nil
	r := append([]netip.AddrPort{ap("10.20.0.5:5432")}, bad...)
	opt.Restriction = &r
	if got := ipRules(BuildNetworkPolicy(app, opt)); len(got) != 1 || !got["10.20.0.5/32:5432"] {
		t.Fatalf("nessuna barriera si apre dalla restrizione: %v", got)
	}
}

// Criterio 2 (H16): con una restrizione l'app raggiunge solo gli indirizzi elencati. Niente regola
// di internet aperto né proxy, restano peer dello stesso repo e DNS.
func TestBuildNetworkPolicy_RestrizionePerApp(t *testing.T) {
	app, _ := testApp()
	r := []netip.AddrPort{ap("93.184.216.34:443"), ap("10.20.0.5:5432")}
	p := BuildNetworkPolicy(app, NetworkPolicyOptions{
		ProxyEnv:        map[string]string{"HTTPS_PROXY": "http://10.9.9.9:3128"},
		PrivateOpenings: []netip.AddrPort{ap("192.168.1.25:587")}, // aperta, ma l'app non la elenca
		Restriction:     &r,
	})
	if internetRuleCount(p) != 0 {
		t.Fatal("con la restrizione non c'è internet aperto")
	}
	got := ipRules(p)
	if len(got) != 2 || !got["93.184.216.34/32:443"] || !got["10.20.0.5/32:5432"] || got["192.168.1.25/32:587"] {
		t.Fatalf("regole: %v", got)
	}
	for _, rule := range p.Spec.Egress {
		for _, to := range rule.To {
			if to.IPBlock != nil && to.IPBlock.CIDR == "10.9.9.9/32" {
				t.Fatal("il proxy non è raggiungibile da un'app con restrizione")
			}
		}
	}
	// Peer del repo (prima regola) e DNS restano.
	if len(p.Spec.Egress) < 2 || p.Spec.Egress[0].To[0].PodSelector == nil || p.Spec.Egress[1].To[0].NamespaceSelector == nil {
		t.Fatalf("peer e DNS: %+v", p.Spec.Egress[:2])
	}
	// Elenco vuoto: nessuna uscita oltre a peer e DNS.
	empty := []netip.AddrPort{}
	p = BuildNetworkPolicy(app, NetworkPolicyOptions{Restriction: &empty})
	if internetRuleCount(p) != 0 || len(ipRules(p)) != 0 || len(p.Spec.Egress) != 2 {
		t.Fatalf("restrizione vuota: %d regole", len(p.Spec.Egress))
	}
}

func fakeResolver(m map[string][]string) Resolver {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		l, ok := m[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []netip.Addr
		for _, s := range l {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestOpenedAddrsERestrictionAddrs(t *testing.T) {
	ctx := context.Background()
	policy := EgressPolicy(nil, []string{"192.168.5.5"}, []string{"10.20.0.77"})
	res := fakeResolver(map[string][]string{
		"db.azienda.lan": {"10.20.0.5"},
		"mixed.lan":      {"10.20.0.6", "127.0.0.1", "10.43.0.1"},
		"pub.example":    {"93.184.216.34"},
	})
	opened := OpenedAddrs(ctx, policy, res, []store.AppPrivateDestination{
		{Host: "db.azienda.lan", Port: 5432},
		{Host: "mixed.lan", Port: 5432},  // solo l'indirizzo buono
		{Host: "gone.lan", Port: 5432},   // non si risolve: nessuna apertura
		{Host: "10.20.0.7", Port: 6443},  // porta di Kubernetes
		{Host: "192.168.5.5", Port: 22},  // nodo
		{Host: "10.20.0.77", Port: 5432}, // lista vietata dell'amministratore
	})
	if len(opened) != 2 || !opened[ap("10.20.0.5:5432")] || !opened[ap("10.20.0.6:5432")] {
		t.Fatalf("aperte: %v", opened)
	}
	got := RestrictionAddrs(ctx, policy, res, opened, []store.AppEgressDestination{
		{Host: "db.azienda.lan", Port: 5432}, // aperta
		{Host: "db.azienda.lan", Port: 5433}, // porta non aperta
		{Host: "10.20.0.99", Port: 5432},     // privato non aperto
		{Host: "pub.example", Port: 443},     // pubblico
		{Host: "mixed.lan", Port: 5432},      // solo l'indirizzo aperto
		{Host: "10.43.0.1", Port: 443},       // cluster
	})
	want := map[netip.AddrPort]bool{ap("10.20.0.5:5432"): true, ap("93.184.216.34:443"): true, ap("10.20.0.6:5432"): true}
	if len(got) != len(want) {
		t.Fatalf("restrizione risolta: %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("indirizzo inatteso %v in %v", g, got)
		}
	}
	// L'amministratore chiude db.azienda.lan: la voce dell'app non vale più.
	if got := RestrictionAddrs(ctx, policy, res, OpenedSet{}, []store.AppEgressDestination{{Host: "db.azienda.lan", Port: 5432}}); len(got) != 0 {
		t.Fatalf("dopo la chiusura: %v", got)
	}
}

func TestCheckAppDestinationEResolve(t *testing.T) {
	policy := EgressPolicy(nil, nil, nil)
	d, _ := egress.NewDestinationPort("kube.lan", 443)
	ips, err := ResolveDestination(context.Background(), fakeResolver(map[string][]string{"kube.lan": {"10.43.0.1"}}), d)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckOpenable(policy, d, ips); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("cluster: %v", err)
	}
	if err := CheckAppDestination(policy, OpenedSet{}, d, ips); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("cluster per un'app: %v", err)
	}
	g, _ := egress.NewDestinationPort("gone.lan", 80)
	if _, err := ResolveDestination(context.Background(), fakeResolver(nil), g); !errors.Is(err, ErrDestinationUnresolved) {
		t.Fatalf("nome inesistente: %v", err)
	}
}

var testProxyEnv = map[string]string{
	"HTTP_PROXY": "http://proxy:3128", "http_proxy": "http://proxy:3128",
	"HTTPS_PROXY": "http://proxy:3128", "https_proxy": "http://proxy:3128",
	"NO_PROXY": ".svc", "no_proxy": ".svc",
}

func deploymentEnvNames(opts BuildOptions) map[string]string {
	app, ver := testApp()
	d := BuildDeployment(app, ver, opts)
	got := map[string]string{}
	for _, e := range d.Spec.Template.Spec.Containers[0].Env {
		got[e.Name] = e.Value
	}
	return got
}

func TestBuildDeployment_AppRistrettaNonRiceveIlProxyN9(t *testing.T) {
	restricted := deploymentEnvNames(BuildOptions{Replicas: 1, ProxyEnv: testProxyEnv, EgressRestricted: true})
	for k := range testProxyEnv {
		if _, ok := restricted[k]; ok {
			t.Errorf("un'app ristretta non deve avere %s: %v", k, restricted)
		}
	}
	free := deploymentEnvNames(BuildOptions{Replicas: 1, ProxyEnv: testProxyEnv})
	for k, v := range testProxyEnv {
		if free[k] != v {
			t.Errorf("un'app libera deve avere %s=%q, ha %q", k, v, free[k])
		}
	}
}

// Accendere o spegnere la restrizione cambia il template del pod (quindi il rollout) in entrambi i versi,
// e l'env torna uguale a quello di prima.
func TestBuildDeployment_CambioDiRestrizioneCambiaIlTemplate(t *testing.T) {
	app, ver := testApp()
	build := func(restricted bool) *appsv1.Deployment {
		return BuildDeployment(app, ver, BuildOptions{Replicas: 1, ProxyEnv: testProxyEnv, EgressRestricted: restricted})
	}
	free, restricted, freeAgain := build(false), build(true), build(false)
	if equality.Semantic.DeepEqual(free.Spec.Template, restricted.Spec.Template) {
		t.Fatal("libera -> ristretta: il template del pod deve cambiare")
	}
	if equality.Semantic.DeepEqual(restricted.Spec.Template, freeAgain.Spec.Template) {
		t.Fatal("ristretta -> libera: il template del pod deve cambiare")
	}
	if !equality.Semantic.DeepEqual(free.Spec.Template, freeAgain.Spec.Template) {
		t.Fatal("spenta la restrizione l'env deve tornare quello di prima")
	}
	// Senza proxy sull'istanza la restrizione non cambia il pod: nessun riavvio inutile.
	a := BuildDeployment(app, ver, BuildOptions{Replicas: 1})
	b := BuildDeployment(app, ver, BuildOptions{Replicas: 1, EgressRestricted: true})
	if !equality.Semantic.DeepEqual(a.Spec.Template, b.Spec.Template) {
		t.Fatal("senza proxy il template non deve cambiare")
	}
}

func TestHasProxy(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{nil, false},
		{map[string]string{"NO_PROXY": ".svc"}, false},
		{map[string]string{"HTTPS_PROXY": "http://proxy:3128"}, true},
		{map[string]string{"http_proxy": "http://10.0.0.5:3128"}, true},
	}
	for _, c := range cases {
		if got := HasProxy(c.env); got != c.want {
			t.Errorf("HasProxy(%v) = %v, atteso %v", c.env, got, c.want)
		}
	}
}
