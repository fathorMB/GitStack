package egress

import (
	"errors"
	"net/netip"
	"testing"
)

func TestParseDestination(t *testing.T) {
	ok := map[string]Destination{
		"db.azienda.lan:5432":    {"db.azienda.lan", 5432},
		" DB.Azienda.LAN.:5432 ": {"db.azienda.lan", 5432},
		"10.0.0.5:5432":          {"10.0.0.5", 5432},
		"[fd00::5]:5432":         {"fd00::5", 5432},
		"[::ffff:10.0.0.5]:25":   {"10.0.0.5", 25},
	}
	for in, want := range ok {
		got, err := ParseDestination(in)
		if err != nil || got != want {
			t.Errorf("ParseDestination(%q) = %+v, %v; voluto %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "db.lan", "db.lan:0", "db.lan:65536", "db.lan:x", "http://db.lan:80", "*.lan:80", "10.0.0.0/8:80", "-a.lan:80", "a..lan:80", ":80", "a b:80"} {
		if d, err := ParseDestination(in); err == nil {
			t.Errorf("ParseDestination(%q) = %+v, voluto un errore", in, d)
		}
	}
	if s := (Destination{"fd00::5", 5432}).String(); s != "[fd00::5]:5432" {
		t.Errorf("String IPv6: %q", s)
	}
}

// CheckOpening: la rete privata si apre, il resto no (H16).
func TestCheckOpening(t *testing.T) {
	p, err := NewPolicy(Config{NodeAddresses: []string{"192.168.5.5"}})
	if err != nil {
		t.Fatal(err)
	}
	open := []struct {
		ip   string
		port uint16
	}{
		{"10.20.0.5", 5432}, {"192.168.1.25", 587}, {"172.16.4.4", 5432}, {"100.64.1.1", 5432}, {"fd00::5", 5432}, {"93.184.216.34", 443},
	}
	for _, c := range open {
		if err := p.CheckOpening(netip.MustParseAddr(c.ip), c.port); err != nil {
			t.Errorf("%s:%d deve potersi aprire: %v", c.ip, c.port, err)
		}
	}
	blocked := []struct {
		ip   string
		port uint16
	}{
		{"127.0.0.1", 5432}, {"::1", 5432}, {"169.254.169.254", 80}, {"fe80::1", 80}, {"0.0.0.0", 80}, {"224.0.0.1", 80},
		{"10.42.1.1", 8080}, {"10.43.0.1", 443}, // cluster
		{"192.168.5.5", 22},                                                                                                          // nodo
		{"10.20.0.5", 6443}, {"10.20.0.5", 10250}, {"10.20.0.5", 10255}, {"10.20.0.5", 2379}, {"10.20.0.5", 2380}, {"fd00::5", 6443}, // Kubernetes
		{"::ffff:127.0.0.1", 80}, // IPv4-mapped: si giudica l'IPv4
	}
	for _, c := range blocked {
		err := p.CheckOpening(netip.MustParseAddr(c.ip), c.port)
		if err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("%s:%d non si deve poter aprire: %v", c.ip, c.port, err)
		}
	}
}

// La lista vietata dell'amministratore vince sull'apertura.
func TestCheckOpening_ListaVietata(t *testing.T) {
	p, err := NewPolicy(Config{Deny: []string{"10.20.0.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CheckOpening(netip.MustParseAddr("10.20.0.5"), 5432); !errors.Is(err, ErrBlocked) {
		t.Errorf("10.20.0.5 è vietato: %v", err)
	}
	if err := p.CheckOpening(netip.MustParseAddr("10.21.0.5"), 5432); err != nil {
		t.Errorf("10.21.0.5: %v", err)
	}
}

// InternetReachable: pubblico sì, privato e speciale no.
func TestInternetReachable(t *testing.T) {
	p, _ := NewPolicy(Config{})
	for ip, want := range map[string]bool{
		"93.184.216.34": true, "2606:2800:220:1::1": true,
		"10.20.0.5": false, "192.168.1.1": false, "172.16.0.1": false, "100.64.0.1": false, "fd00::1": false,
		"127.0.0.1": false, "169.254.169.254": false, "10.43.0.1": false, "203.0.113.9": false,
	} {
		if got := p.InternetReachable(netip.MustParseAddr(ip), 443); got != want {
			t.Errorf("InternetReachable(%s) = %v, voluto %v", ip, got, want)
		}
	}
	// Anche con una lista ammessa che elenca il privato, resta chiuso senza apertura precisa.
	q, _ := NewPolicy(Config{Allow: []string{"10.0.0.0/8"}})
	if q.InternetReachable(netip.MustParseAddr("10.20.0.5"), 443) {
		t.Error("la lista ammessa non apre la rete privata alle app")
	}
	// Porta di Kubernetes su un indirizzo pubblico: non è una porta del cluster, passa.
	if !p.InternetReachable(netip.MustParseAddr("93.184.216.34"), 6443) {
		t.Error("6443 su un indirizzo pubblico non è una porta di questo cluster")
	}
}
