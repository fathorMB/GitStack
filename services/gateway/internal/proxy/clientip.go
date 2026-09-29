package proxy

import (
	"net"
	"net/http"
	"strings"
)

// Option configura ToCore.
type Option func(*options)

type options struct {
	trusted []*net.IPNet
}

// WithTrustedProxies indica le reti (CIDR) dei proxy davanti al gateway
// (es. Traefik) di cui ci si fida per X-Forwarded-For. Senza questa opzione
// nessun peer è fidato e l'IP del client è sempre quello della connessione.
func WithTrustedProxies(nets []*net.IPNet) Option {
	return func(o *options) { o.trusted = nets }
}

func isTrusted(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP ricava l'IP del client. Se la connessione (RemoteAddr) non arriva
// da un proxy fidato, X-Forwarded-For è ignorato e vale RemoteAddr. Se arriva
// da un proxy fidato si scorre X-Forwarded-For da destra a sinistra e si
// prende il primo indirizzo non fidato (i proxy fidati a destra sono hop
// intermedi; ciò che sta a sinistra di un indirizzo non fidato è
// falsificabile dal client). Un valore malformato invalida l'header: si
// ricade su RemoteAddr. Se tutti gli hop sono fidati vale il più a sinistra.
// Ritorna "" se RemoteAddr non è un IP.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return ""
	}
	if !isTrusted(trusted, peer) {
		return peer.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	if len(hops) == 0 {
		return peer.String()
	}
	parsed := make([]net.IP, len(hops))
	for i, h := range hops {
		ip := net.ParseIP(strings.TrimSpace(h))
		if ip == nil {
			return peer.String()
		}
		parsed[i] = ip
	}
	for i := len(parsed) - 1; i >= 0; i-- {
		if !isTrusted(trusted, parsed[i]) {
			return parsed[i].String()
		}
	}
	return parsed[0].String()
}
