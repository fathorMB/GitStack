package mirrors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/fathorMB/GitStack/pkg/egress"
)

// ErrURLNotAllowed: l'indirizzo non è ammesso (422 url_not_allowed).
var ErrURLNotAllowed = errors.New("indirizzo non ammesso")

// Egress applica la policy di uscita dell'installazione (pkg/egress, C8) alla
// destinazione di un mirror. A differenza di un webhook, qui non c'è un
// client HTTP di pkg/egress che giudica l'indirizzo al dial: core risolve il
// nome, controlla OGNI indirizzo e passa al servizio git quello scelto, che lo
// fissa per la connessione. Le regole sono le stesse (values.yaml egress.*):
// loopback, link-local, cluster e intervalli speciali sempre bloccati, deny
// dell'amministratore, allow che restringe e sblocca.
//
// pkg/egress giudica un IP: le voci per NOME (`example.com`, `*.example.com`)
// di allow e deny le applica questo tipo all'host dell'URL.
type Egress struct {
	// base: deny e cluster, senza allow (le voci di allow le legge Check).
	base *egress.Policy
	// full: la policy completa, per gli IP elencati in allow.
	full *egress.Policy

	allow, deny []string
	// Resolve risolve un nome in indirizzi; default il resolver di sistema.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// NewEgress costruisce il controllo dalle liste del chart.
func NewEgress(allow, deny, clusterCIDRs []string) (*Egress, error) {
	base, err := egress.NewPolicy(egress.Config{Deny: deny, ClusterCIDRs: clusterCIDRs})
	if err != nil {
		return nil, fmt.Errorf("mirrors: policy di uscita non valida: %w", err)
	}
	full, err := egress.NewPolicy(egress.Config{Allow: allow, Deny: deny, ClusterCIDRs: clusterCIDRs})
	if err != nil {
		return nil, fmt.Errorf("mirrors: policy di uscita non valida: %w", err)
	}
	e := &Egress{base: base, full: full}
	for _, a := range allow {
		if a = strings.TrimSpace(a); a != "" {
			e.allow = append(e.allow, strings.ToLower(a))
		}
	}
	for _, d := range deny {
		if d = strings.TrimSpace(d); d != "" {
			e.deny = append(e.deny, strings.ToLower(d))
		}
	}
	return e, nil
}

// isName: la voce non è un IP né un CIDR.
func isName(entry string) bool {
	if _, err := netip.ParseAddr(entry); err == nil {
		return false
	}
	if _, err := netip.ParsePrefix(entry); err == nil {
		return false
	}
	return true
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func matchName(list []string, host string) bool {
	host = normalizeHost(host)
	for _, entry := range list {
		if !isName(entry) {
			continue
		}
		entry = normalizeHost(entry)
		if suffix, ok := strings.CutPrefix(entry, "*"); ok {
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

func blocked(format string, args ...any) error {
	return fmt.Errorf("%w: %s", egress.ErrBlocked, fmt.Sprintf(format, args...))
}

// CheckIP giudica un indirizzo per l'host dato.
func (e *Egress) CheckIP(host string, ip netip.Addr) error {
	if matchName(e.deny, host) {
		return blocked("%s è vietato dall'amministratore", host)
	}
	hasAllow := len(e.allow) > 0
	if !hasAllow || matchName(e.allow, host) {
		return e.base.CheckIP(ip)
	}
	// Allow restringe, e l'host non è fra i nomi ammessi: passa solo un IP
	// elencato in allow.
	return e.full.CheckIP(ip)
}

// CheckURL è la validazione alla creazione e alla modifica: https, host,
// niente credenziali, né localhost né IP letterali bloccati e nomi vietati.
// I nomi non si risolvono qui (cambiano nel tempo): lo fa Resolve a ogni push.
func (e *Egress) CheckURL(raw string) error {
	u, err := ParseURL(raw)
	if err != nil {
		return err
	}
	host := normalizeHost(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: localhost è bloccato", ErrURLNotAllowed)
	}
	if matchName(e.deny, host) {
		return fmt.Errorf("%w: %s è vietato dall'amministratore", ErrURLNotAllowed, host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := e.CheckIP(host, ip); err != nil {
			return fmt.Errorf("%w: %s è bloccato", ErrURLNotAllowed, host)
		}
	}
	return nil
}

// ParseURL accetta solo https://host[:porta]/percorso, senza credenziali,
// query o frammento, al massimo 2048 caratteri.
func ParseURL(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 2048 {
		return nil, fmt.Errorf("%w: da 1 a 2048 caratteri", ErrURLNotAllowed)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: serve un indirizzo https", ErrURLNotAllowed)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: niente credenziali nell'indirizzo", ErrURLNotAllowed)
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, " \t\r\n") {
		return nil, fmt.Errorf("%w: niente query, frammento né spazi", ErrURLNotAllowed)
	}
	if p := u.Port(); p != "" {
		n := 0
		for _, c := range p {
			n = n*10 + int(c-'0')
		}
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("%w: porta non valida", ErrURLNotAllowed)
		}
	}
	return u, nil
}

// Pick risolve l'host dell'URL, controlla OGNI indirizzo e ritorna quello da
// usare (preferendo IPv4). Un indirizzo bloccato blocca tutto: un nome che
// risponde anche con un IP interno non si usa. L'errore di blocco avvolge
// egress.ErrBlocked (egress.IsBlocked), uno di rete no.
func (e *Egress) Pick(ctx context.Context, u *url.URL) (netip.Addr, error) {
	host := normalizeHost(u.Hostname())
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		resolve := e.Resolve
		if resolve == nil {
			resolve = systemResolve
		}
		addrs, err = resolve(ctx, host)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("risoluzione di %s non riuscita: %w", host, err)
		}
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("%s non ha indirizzi", host)
	}
	var pick netip.Addr
	for _, a := range addrs {
		a = a.WithZone("").Unmap()
		if err := e.CheckIP(host, a); err != nil {
			return netip.Addr{}, err
		}
		if !pick.IsValid() || (a.Is4() && !pick.Is4()) {
			pick = a
		}
	}
	return pick, nil
}

func systemResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}
