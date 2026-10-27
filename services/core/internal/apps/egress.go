package apps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Uscita delle app (H16, GIT-385). Internet è aperto, la rete privata è chiusa. L'amministratore
// dell'installazione apre destinazioni private precise (host:porta); chi ha admin sull'app può
// restringere la sua uscita a un elenco, mai oltre quanto l'amministratore permette. Le regole di
// fondo (loopback, link-local, cluster, nodo, porte di Kubernetes) sono quelle di pkg/egress e
// nessuna apertura le supera: l'API rifiuta la voce e il controller la ricontrolla prima di scrivere
// la NetworkPolicy.

// Resolver risolve un nome DNS negli indirizzi IP: core lo usa sia per validare le voci sia per
// scrivere gli indirizzi nella NetworkPolicy (che lavora sugli IP).
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// DefaultResolver usa il resolver del sistema.
func DefaultResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, len(ips))
	for i, ip := range ips {
		out[i] = ip.Unmap().WithZone("")
	}
	return out, nil
}

// EgressPolicy è la policy d'istanza delle barriere: cluster e nodo come per webhook e mirror. Una
// configurazione non valida torna ai default di pkg/egress (mai meno barriere). deny è la lista vietata
// dell'amministratore (GITSTACK_EGRESS_DENY): una destinazione vietata non si apre.
func EgressPolicy(clusterCIDRs, nodeAddresses, deny []string) *egress.Policy {
	p, err := egress.NewPolicy(egress.Config{ClusterCIDRs: clusterCIDRs, NodeAddresses: nodeAddresses, Deny: deny})
	if err != nil {
		p, _ = egress.NewPolicy(egress.Config{})
	}
	return p
}

// ErrDestinationUnresolved: il nome non si risolve in nessun indirizzo.
var ErrDestinationUnresolved = errors.New("il nome non si risolve in nessun indirizzo")

// ResolveDestination dà gli indirizzi di una destinazione: l'IP stesso, o la risoluzione del nome.
func ResolveDestination(ctx context.Context, resolve Resolver, d egress.Destination) ([]netip.Addr, error) {
	if ip, ok := d.IP(); ok {
		return []netip.Addr{ip}, nil
	}
	if resolve == nil {
		resolve = DefaultResolver
	}
	ips, err := resolve(ctx, d.Host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrDestinationUnresolved, d.Host)
	}
	return ips, nil
}

// CheckOpenable: l'amministratore può aprire la destinazione solo se ogni indirizzo in cui si risolve
// passa le regole di fondo (egress.Policy.CheckOpening). L'errore avvolge egress.ErrBlocked.
func CheckOpenable(policy *egress.Policy, d egress.Destination, ips []netip.Addr) error {
	for _, ip := range ips {
		if err := policy.CheckOpening(ip, d.Port); err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
	}
	return nil
}

// OpenedSet sono gli indirizzi:porta aperti dall'amministratore, già risolti e filtrati.
type OpenedSet map[netip.AddrPort]bool

// OpenedAddrs risolve le destinazioni aperte dall'amministratore. Una voce che oggi non si risolve o
// che passa le regole di fondo solo in parte apre solo gli indirizzi buoni: mai un indirizzo
// vietato.
func OpenedAddrs(ctx context.Context, policy *egress.Policy, resolve Resolver, opened []store.AppPrivateDestination) OpenedSet {
	set := OpenedSet{}
	for _, o := range opened {
		d, err := egress.NewDestinationPort(o.Host, uint16(o.Port))
		if err != nil {
			continue
		}
		ips, err := ResolveDestination(ctx, resolve, d)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if policy.CheckOpening(ip, d.Port) == nil {
				set[netip.AddrPortFrom(ip, d.Port)] = true
			}
		}
	}
	return set
}

// CheckAppDestination: una voce della restrizione di un'app è ammessa se ogni indirizzo è
// raggiungibile da qualunque app (internet) oppure è aperto dall'amministratore. L'errore avvolge
// egress.ErrBlocked.
func CheckAppDestination(policy *egress.Policy, opened OpenedSet, d egress.Destination, ips []netip.Addr) error {
	for _, ip := range ips {
		if policy.InternetReachable(ip, d.Port) {
			continue
		}
		if err := policy.CheckOpening(ip, d.Port); err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
		if !opened[netip.AddrPortFrom(ip, d.Port)] {
			return fmt.Errorf("%s: %w: %s è nella rete privata e l'amministratore non l'ha aperta", d, egress.ErrBlocked, ip)
		}
	}
	return nil
}

// RestrictionAddrs risolve la restrizione di un'app negli indirizzi per la NetworkPolicy, tenendo
// solo quelli ammessi (CheckAppDestination indirizzo per indirizzo). Sempre non nil: una
// restrizione senza indirizzi utili chiude l'uscita dell'app.
func RestrictionAddrs(ctx context.Context, policy *egress.Policy, resolve Resolver, opened OpenedSet, list []store.AppEgressDestination) []netip.AddrPort {
	out := []netip.AddrPort{}
	for _, e := range list {
		d, err := egress.NewDestinationPort(e.Host, uint16(e.Port))
		if err != nil {
			continue
		}
		ips, err := ResolveDestination(ctx, resolve, d)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if CheckAppDestination(policy, opened, d, []netip.Addr{ip}) == nil {
				out = append(out, netip.AddrPortFrom(ip, d.Port))
			}
		}
	}
	return out
}

func sortedAddrPorts(set OpenedSet) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(set))
	for ap := range set {
		out = append(out, ap)
	}
	sort.Slice(out, func(i, j int) bool {
		if c := out[i].Addr().Compare(out[j].Addr()); c != 0 {
			return c < 0
		}
		return out[i].Port() < out[j].Port()
	})
	return out
}
