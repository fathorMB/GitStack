// Package egress protects outgoing HTTP calls to user-supplied addresses
// (webhooks, rule C8) from SSRF. The decision is taken on the address that is
// actually dialed, after DNS resolution and for every redirect hop.
package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// ErrBlocked is wrapped by every error caused by the egress policy.
var ErrBlocked = errors.New("egress: destination blocked")

// DefaultClusterCIDRs are the k3s defaults for pods and services.
var DefaultClusterCIDRs = []string{"10.42.0.0/16", "10.43.0.0/16"}

// Config is the installation-wide policy, set by the administrator.
type Config struct {
	// Allow restricts (hostnames, empty = no restriction) and widens (IP/CIDR
	// entries unlock the "special" ranges) the destinations. Entries are IPs,
	// CIDRs, host names ("example.com") or suffix patterns ("*.example.com").
	Allow []string
	// Deny lists destinations that are always refused; it wins over Allow.
	Deny []string
	// ClusterCIDRs are always blocked. Nil means DefaultClusterCIDRs; an
	// empty non-nil slice is treated like nil (the cluster is never exposed
	// by omission).
	ClusterCIDRs []string
}

type entries struct {
	prefixes []netip.Prefix
	hosts    []string // exact names
	suffixes []string // ".example.com" from "*.example.com"
}

func (e entries) empty() bool {
	return len(e.prefixes) == 0 && len(e.hosts) == 0 && len(e.suffixes) == 0
}

func (e entries) matchIP(ip netip.Addr) bool {
	for _, p := range e.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (e entries) matchHost(host string) bool {
	host = normalizeHost(host)
	for _, h := range e.hosts {
		if h == host {
			return true
		}
	}
	for _, s := range e.suffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func parseEntries(list []string) (entries, error) {
	var e entries
	for _, raw := range list {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			e.prefixes = append(e.prefixes, normPrefix(p))
			continue
		}
		if a, err := netip.ParseAddr(s); err == nil {
			a = a.Unmap().WithZone("")
			e.prefixes = append(e.prefixes, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		if strings.Contains(s, "/") || strings.ContainsAny(s, " :") {
			return e, fmt.Errorf("egress: invalid entry %q", raw)
		}
		if rest, ok := strings.CutPrefix(s, "*."); ok && rest != "" && !strings.Contains(rest, "*") {
			e.suffixes = append(e.suffixes, "."+normalizeHost(rest))
			continue
		}
		if strings.Contains(s, "*") {
			return e, fmt.Errorf("egress: invalid entry %q", raw)
		}
		e.hosts = append(e.hosts, normalizeHost(s))
	}
	return e, nil
}

// normPrefix maps IPv4-mapped IPv6 prefixes (::ffff:a.b.c.d/n) to IPv4.
func normPrefix(p netip.Prefix) netip.Prefix {
	a := p.Addr()
	if a.Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(a.Unmap(), p.Bits()-96).Masked()
	}
	return p.Masked()
}

// Policy decides whether an address may be dialed. It is immutable.
type Policy struct {
	allow, deny, cluster entries
}

// NewPolicy validates cfg and builds a Policy.
func NewPolicy(cfg Config) (*Policy, error) {
	p := &Policy{}
	var err error
	if p.allow, err = parseEntries(cfg.Allow); err != nil {
		return nil, fmt.Errorf("allow: %w", err)
	}
	if p.deny, err = parseEntries(cfg.Deny); err != nil {
		return nil, fmt.Errorf("deny: %w", err)
	}
	cc := cfg.ClusterCIDRs
	if len(cc) == 0 {
		cc = DefaultClusterCIDRs
	}
	if p.cluster, err = parseEntries(cc); err != nil {
		return nil, fmt.Errorf("clusterCIDRs: %w", err)
	}
	if len(p.cluster.hosts)+len(p.cluster.suffixes) > 0 {
		return nil, errors.New("clusterCIDRs: only IPs and CIDRs are allowed")
	}
	return p, nil
}

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// Never unlockable by Allow.
var hardBlocked = []netip.Prefix{
	pfx("0.0.0.0/8"),      // "this network", includes 0.0.0.0
	pfx("127.0.0.0/8"),    // loopback
	pfx("169.254.0.0/16"), // link-local (cloud metadata 169.254.169.254)
	pfx("224.0.0.0/4"),    // multicast
	pfx("240.0.0.0/4"),    // reserved + broadcast 255.255.255.255
	pfx("::/96"),          // unspecified, loopback, IPv4-compatible
	pfx("fe80::/10"),      // link-local
	pfx("ff00::/8"),       // multicast
	pfx("fec0::/10"),      // deprecated site-local
	pfx("::ffff:0:0/96"),  // IPv4-mapped (unmapped before checking; belt and braces)
	pfx("64:ff9b:1::/48"), // local-use NAT64
}

// Blocked by default, can be unlocked with an IP/CIDR Allow entry.
var softBlocked = []netip.Prefix{
	pfx("100.64.0.0/10"),   // CGNAT
	pfx("192.0.0.0/24"),    // IETF protocol assignments
	pfx("192.0.2.0/24"),    // TEST-NET-1
	pfx("198.18.0.0/15"),   // benchmarking
	pfx("198.51.100.0/24"), // TEST-NET-2
	pfx("203.0.113.0/24"),  // TEST-NET-3
	pfx("192.88.99.0/24"),  // 6to4 relay anycast (deprecated)
	pfx("100::/64"),        // discard-only
	pfx("2001::/32"),       // Teredo
	pfx("2001:db8::/32"),   // documentation
	pfx("2002::/16"),       // 6to4
}

var nat64 = pfx("64:ff9b::/96")

func canonical(ip netip.Addr) netip.Addr {
	return ip.WithZone("").Unmap()
}

func inAny(list []netip.Prefix, ip netip.Addr) bool {
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckIP reports whether ip may be dialed, ignoring host name rules.
// The returned error wraps ErrBlocked.
func (p *Policy) CheckIP(ip netip.Addr) error {
	return p.check("", ip)
}

func (p *Policy) check(host string, ip netip.Addr) error {
	ip = canonical(ip)
	if !ip.IsValid() {
		return fmt.Errorf("%w: invalid address", ErrBlocked)
	}
	if nat64.Contains(ip) { // judge the embedded IPv4
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	if inAny(hardBlocked, ip) || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %s is loopback, link-local or non-routable", ErrBlocked, ip)
	}
	if p.cluster.matchIP(ip) {
		return fmt.Errorf("%w: %s is inside the cluster network", ErrBlocked, ip)
	}
	if p.deny.matchIP(ip) || (host != "" && p.deny.matchHost(host)) {
		return fmt.Errorf("%w: %s is denied by the administrator", ErrBlocked, ip)
	}
	ipAllowed := p.allow.matchIP(ip)
	if inAny(softBlocked, ip) && !ipAllowed {
		return fmt.Errorf("%w: %s is in a special-purpose range", ErrBlocked, ip)
	}
	if !p.allow.empty() && !ipAllowed && (host == "" || !p.allow.matchHost(host)) {
		return fmt.Errorf("%w: %s is not in the administrator allow list", ErrBlocked, ip)
	}
	return nil
}
