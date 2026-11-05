package databases

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

// DB16: il Service della LAN e' un LoadBalancer sulla porta assegnata (ServiceLB di k3s) verso la 5432.
func TestManifests_LANService(t *testing.T) {
	d := testDB()
	svc := BuildLANService(d, 15433)
	if svc.Name != LANServiceName || svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Fatalf("Service: %s %s", svc.Name, svc.Spec.Type)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 15433 || svc.Spec.Ports[0].TargetPort.IntValue() != PostgresPort {
		t.Fatalf("porte: %+v", svc.Spec.Ports)
	}
	if svc.Spec.Selector[LabelDBID] != d.ID.String() {
		t.Fatalf("selector: %v", svc.Spec.Selector)
	}
	// Il Service interno (ClusterIP) non cambia: l'accesso delle app non passa dalla LAN.
	if in := BuildService(d); in.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("Service interno: %s", in.Spec.Type)
	}
}

// DB16/DB11: db-lan ammette la 5432 da fuori dal CIDR dei pod e non apre nient'altro: la rete delle app
// resta fuori (le app collegate passano da db-isolation) e non c'e' uscita.
func TestManifests_LANNetworkPolicy(t *testing.T) {
	d := testDB()
	for _, c := range []struct{ in, want string }{{"", DefaultPodCIDR}, {"10.99.0.0/16", "10.99.0.0/16"}} {
		np := BuildLANNetworkPolicy(d, c.in)
		if np.Name != LANNetworkPolicyName || len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
			t.Fatalf("policy: %+v", np.Spec)
		}
		if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].From) != 1 || len(np.Spec.Ingress[0].Ports) != 1 {
			t.Fatalf("regole: %+v", np.Spec.Ingress)
		}
		blk := np.Spec.Ingress[0].From[0].IPBlock
		if blk == nil || blk.CIDR != "0.0.0.0/0" || len(blk.Except) != 1 || blk.Except[0] != c.want {
			t.Fatalf("ipBlock: %+v, voluto except %s", blk, c.want)
		}
		if np.Spec.Ingress[0].Ports[0].Port.IntValue() != PostgresPort {
			t.Fatalf("porta: %v", np.Spec.Ingress[0].Ports[0].Port)
		}
	}
	// db-isolation non cambia con la LAN: ingresso solo dai namespace collegati, nessuna uscita.
	iso := BuildNetworkPolicy(d)
	if iso.Spec.Ingress[0].From[0].IPBlock != nil || len(iso.Spec.Egress) != 0 {
		t.Fatalf("db-isolation alterata: %+v", iso.Spec)
	}
}

// DB17: la foglia con la LAN accesa porta i nomi e gli IP dell'istanza (quelli dell'intermedia) e passa
// verify-full contro la radice sia per il nome DNS sia per l'IP; il loopback e *.svc.cluster.local non
// finiscono nei SAN della LAN; un nome fuori dall'istanza non passa.
func TestPKI_LANSANDeiNomiDellIstanza(t *testing.T) {
	root, caDir := writeTestCA(t)
	dns, ips, err := LANNames(caDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dns) != 1 || dns[0] != "gitstack.test" || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 10)) {
		t.Fatalf("nomi dell'istanza: %v %v", dns, ips)
	}
	d := testDB()
	cert, err := IssueServerCert(caDir, append([]string{ServiceHost(d.ID)}, dns...), ips, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var chain []*x509.Certificate
	rest := cert.ChainPEM
	for {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
	}
	if len(chain) != 2 {
		t.Fatalf("catena: %d certificati", len(chain))
	}
	inter, roots := x509.NewCertPool(), x509.NewCertPool()
	inter.AddCert(chain[1])
	roots.AddCert(root)
	for _, name := range []string{"gitstack.test", "192.0.2.10", ServiceHost(d.ID)} {
		if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: name}); err != nil {
			t.Errorf("verify-full su %s: %v", name, err)
		}
	}
	for _, name := range []string{"altro.example.com", "192.0.2.99"} {
		if _, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: name}); err == nil {
			t.Errorf("%s non deve passare verify-full", name)
		}
	}
	// Senza la radice (CA di sistema) la catena non e' valida: e' il «certificate verify failed» del client.
	if _, err := chain[0].Verify(x509.VerifyOptions{Intermediates: inter, DNSName: "gitstack.test"}); err == nil {
		t.Error("senza la radice privata la catena non deve essere valida")
	}
}

func TestPKI_LANNamesSenzaCA(t *testing.T) {
	if _, _, err := LANNames(t.TempDir()); err == nil {
		t.Fatal("senza CA serve un errore")
	}
}

// sanKey non dipende dall'ordine: serve a decidere se riemettere la foglia.
func TestSanKey(t *testing.T) {
	a := sanKey([]string{"b", "a"}, []net.IP{net.ParseIP("10.0.0.2"), net.ParseIP("10.0.0.1")})
	b := sanKey([]string{"a", "b"}, []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2")})
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if sanKey([]string{"a"}, nil) == sanKey([]string{"a", "b"}, nil) {
		t.Fatal("SAN diversi, chiave uguale")
	}
}

func TestPickLANHost(t *testing.T) {
	dns := []string{"gitstack.lan"}
	ips := []net.IP{net.ParseIP("192.0.2.10")}
	for _, c := range []struct{ in, want string }{
		{"gitstack.lan:443", "gitstack.lan"},
		{"GitStack.LAN", "gitstack.lan"},
		{"192.0.2.10:8443", "192.0.2.10"},
		{"[::1]:443", "gitstack.lan"}, // non e' dell'istanza: il primo nome
		{"evil.example.com", "gitstack.lan"},
		{"", "gitstack.lan"},
	} {
		if got := pickLANHost(c.in, dns, ips); got != c.want {
			t.Errorf("pickLANHost(%q) = %q, voluto %q", c.in, got, c.want)
		}
	}
	if got := pickLANHost("x", nil, ips); got != "192.0.2.10" {
		t.Errorf("installazione con solo IP: %q", got)
	}
	if got := pickLANHost("x", nil, nil); got != "" {
		t.Errorf("senza nomi: %q", got)
	}
}

func TestUFWCommand(t *testing.T) {
	if got := UFWCommand(15440); got != "ufw allow from <rete> to any port 15440 proto tcp" {
		t.Fatal(got)
	}
}

func TestValidatePortRange(t *testing.T) {
	for _, c := range []struct {
		from, to int
		ok       bool
	}{{15432, 15531, true}, {1024, 1024, true}, {1023, 2000, false}, {2000, 1999, false}, {2000, 65536, false}} {
		if err := ValidatePortRange(c.from, c.to); (err == nil) != c.ok {
			t.Errorf("%d-%d: %v", c.from, c.to, err)
		}
	}
}

// Le porte dell'installazione non si assegnano mai, anche dentro l'intervallo.
func TestLANPortPolicy_RiservateNonAssegnate(t *testing.T) {
	c := &Controller{cfg: Config{LANPortFrom: 2220, LANPortTo: 2230, ReservedPorts: []int{2224}}}
	pol := c.LANPortPolicy()
	used := map[int]bool{}
	var got []int
	for i := 0; i < 20; i++ {
		p, ok := pol.Pick(used)
		if !ok {
			break
		}
		used[p] = true
		got = append(got, p)
	}
	for _, p := range got {
		if p == 2222 || p == 2224 {
			t.Fatalf("assegnata una porta riservata: %v", got)
		}
	}
	if len(got) != 9 {
		t.Fatalf("11 porte meno 2222 e 2224 = 9: %v", got)
	}
	if def := (&Controller{}).LANPortPolicy(); def.From != DefaultLANPortFrom || def.To != DefaultLANPortTo {
		t.Fatalf("default: %+v", def)
	}
}
