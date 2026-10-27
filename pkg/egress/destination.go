package egress

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Destination è una destinazione precisa di uscita, «host:porta» (H16): un indirizzo IP o un nome DNS
// e una porta. Sono le voci che l'amministratore apre sulla rete privata e quelle con cui un'app
// restringe la propria uscita.
type Destination struct {
	Host string
	Port uint16
}

// String dà «host:porta», con le parentesi quadre per un IPv6.
func (d Destination) String() string {
	return net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port)))
}

// ParseDestination legge «host:porta». L'host è un IP o un nome DNS (niente schema, percorso, CIDR o
// caratteri jolly); la porta è obbligatoria, da 1 a 65535.
func ParseDestination(s string) (Destination, error) {
	s = strings.TrimSpace(s)
	host, portText, err := net.SplitHostPort(s)
	if err != nil {
		return Destination{}, fmt.Errorf("egress: %q non è nella forma host:porta", s)
	}
	return NewDestination(host, portText)
}

// NewDestination valida host e porta (testo).
func NewDestination(host, portText string) (Destination, error) {
	port, err := strconv.ParseUint(strings.TrimSpace(portText), 10, 16)
	if err != nil || port == 0 {
		return Destination{}, fmt.Errorf("egress: la porta %q non è valida (1-65535)", portText)
	}
	return NewDestinationPort(host, uint16(port))
}

// NewDestinationPort valida l'host e normalizza il nome (minuscolo, senza punto finale).
func NewDestinationPort(host string, port uint16) (Destination, error) {
	host = strings.TrimSpace(host)
	if port == 0 {
		return Destination{}, fmt.Errorf("egress: la porta deve essere tra 1 e 65535")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return Destination{Host: a.Unmap().WithZone("").String(), Port: port}, nil
	}
	h := normalizeHost(host)
	if !validHostName(h) {
		return Destination{}, fmt.Errorf("egress: %q non è un indirizzo IP né un nome DNS valido", host)
	}
	return Destination{Host: h, Port: port}, nil
}

func validHostName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

// IP dice se l'host è un indirizzo IP e, in quel caso, quale.
func (d Destination) IP() (netip.Addr, bool) {
	a, err := netip.ParseAddr(d.Host)
	return a, err == nil
}

// CheckOpening dice se l'amministratore può aprire ip:porta sulla rete privata: vale ciò che
// vale per ogni uscita (loopback, link-local, multicast e riservati, rete del cluster, indirizzi del
// nodo, porte di Kubernetes su indirizzi privati, lista vietata) ma gli intervalli privati e speciali
// non bloccano, perché aprirli è proprio lo scopo. La lista ammessa non si applica: la voce stessa
// è l'apertura. L'errore avvolge ErrBlocked.
func (p *Policy) CheckOpening(ip netip.Addr, port uint16) error {
	q := *p
	ip = canonical(ip)
	q.allow = entries{prefixes: []netip.Prefix{netip.PrefixFrom(ip, ip.BitLen())}}
	return q.checkPort("", ip, port)
}

// InternetReachable dice se ip:porta è raggiungibile da ogni app senza nessuna apertura (H16): è un
// indirizzo pubblico che la policy lascia passare. Le reti private (RFC 1918, ULA, CGNAT) e gli
// intervalli speciali non lo sono, qualunque cosa dica la lista ammessa: si aprono solo con una voce
// precisa dell'amministratore (CheckOpening).
func (p *Policy) InternetReachable(ip netip.Addr, port uint16) bool {
	ip = canonical(ip)
	if ip.IsValid() && nat64.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	if !ip.IsValid() || isPrivate(ip) || inAny(softBlocked, ip) {
		return false
	}
	return p.CheckAddrPort(ip, port) == nil
}
