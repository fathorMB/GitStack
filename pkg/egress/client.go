package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

const (
	// MaxLoggedBody is the maximum number of response bytes kept for logs.
	MaxLoggedBody = 4096
	// DefaultMaxRedirects is the redirect limit of the client.
	DefaultMaxRedirects = 5
	// DefaultTimeout is the whole-request timeout of the client.
	DefaultTimeout = 10 * time.Second
)

// Options tunes NewClient. The zero value is valid.
type Options struct {
	Timeout      time.Duration // default DefaultTimeout
	MaxRedirects int           // default DefaultMaxRedirects; <0 disables redirects
	// Proxy è il proxy aziendale (N9); nil = connessione diretta. La policy si
	// applica comunque: con un proxy, alla destinazione vera prima di aprire
	// la richiesta (vedi ProxyConfig).
	Proxy *ProxyConfig

	// lookup and connect are hooks for tests only.
	lookup  func(ctx context.Context, host string) ([]netip.Addr, error)
	connect func(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error)
}

// NewClient returns an http.Client whose every connection (first request and
// every redirect hop) is checked by policy on the resolved IP.
func NewClient(policy *Policy, opts Options) *http.Client {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects == 0 {
		maxRedirects = DefaultMaxRedirects
	}
	if maxRedirects < 0 {
		maxRedirects = 0
	}
	lookup := opts.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	connect := opts.connect
	if connect == nil {
		connect = func(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		}
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if viaProxy(ctx) {
			// La connessione al proxy (non alla destinazione): la destinazione
			// è già stata giudicata da checkedTransport prima di arrivare qui.
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if policy.deny.matchHost(host) {
			return nil, fmt.Errorf("%w: host %s is denied by the administrator", ErrBlocked, host)
		}
		// Control runs after resolution, right before connect(2), and knows the
		// request host: the check that DNS rebinding cannot bypass.
		dialer := &net.Dialer{Timeout: 5 * time.Second, Control: policy.controlFor(host)}
		var ips []netip.Addr
		if ip, perr := netip.ParseAddr(host); perr == nil {
			ips = []netip.Addr{ip}
		} else if ips, err = lookup(ctx, host); err != nil {
			return nil, err
		}
		var lastErr error = &net.DNSError{Err: "no addresses", Name: host}
		for _, ip := range ips {
			if err := policy.check(host, ip); err != nil {
				lastErr = err
				continue
			}
			target := net.JoinHostPort(canonical(ip).String(), port)
			c, err := connect(ctx, dialer, network, target)
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	tr := &http.Transport{
		// Mai ProxyFromEnvironment: il proxy è solo quello di Options.Proxy, e la
		// destinazione si giudica prima (checkedTransport).
		Proxy: func(req *http.Request) (*url.URL, error) { return opts.Proxy.For(req.URL), nil },
		DialContext:           dialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: opts.Timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	var rt http.RoundTripper = tr
	if opts.Proxy != nil {
		rt = &checkedTransport{base: tr, policy: policy, proxy: opts.Proxy, lookup: lookup}
	}
	return &http.Client{
		Transport: rt,
		Timeout:   opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("egress: stopped after %d redirects", maxRedirects)
			}
			if s := req.URL.Scheme; s != "http" && s != "https" {
				return fmt.Errorf("%w: redirect to scheme %q", ErrBlocked, s)
			}
			return nil // the address is judged by the dialer
		},
	}
}

// controlFor returns a net.Dialer Control that judges the address actually
// being connected, knowing the host name of the request (for name rules).
func (p *Policy) controlFor(host string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		switch network {
		case "tcp", "tcp4", "tcp6":
		default:
			return fmt.Errorf("%w: network %q", ErrBlocked, network)
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: unparsable address %q", ErrBlocked, address)
		}
		return p.check(host, ap.Addr())
	}
}

// ReadBody reads at most MaxLoggedBody bytes of r, for the delivery log. It
// reports whether the body was longer. A nil r yields an empty result.
func ReadBody(r io.Reader) (body []byte, truncated bool, err error) {
	if r == nil {
		return nil, false, nil
	}
	body, err = io.ReadAll(io.LimitReader(r, MaxLoggedBody+1))
	if len(body) > MaxLoggedBody {
		return body[:MaxLoggedBody], true, err
	}
	return body, false, err
}

// IsBlocked reports whether err was caused by the egress policy.
func IsBlocked(err error) bool { return errors.Is(err, ErrBlocked) }

type proxyCtxKey struct{}

func viaProxy(ctx context.Context) bool { return ctx.Value(proxyCtxKey{}) != nil }

// checkedTransport giudica la destinazione vera di ogni richiesta (anche di
// ogni redirect) PRIMA che il transport apra la connessione al proxy: deny per
// nome, poi la risoluzione e la policy su OGNI indirizzo (loopback, link-local,
// cluster e reti speciali restano bloccati anche via proxy). Una destinazione
// rifiutata non fa mai contattare il proxy. Le destinazioni in NO_PROXY vanno
// dirette: le giudica il dialer come senza proxy.
//
// Limite: il proxy risolve il nome per conto suo, quindi fra controllo e
// connessione una risposta DNS diversa non è visibile (nessun TOCTOU da
// chiudere finché il proxy è quello aziendale e fidato).
type checkedTransport struct {
	base   *http.Transport
	policy *Policy
	proxy  *ProxyConfig
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
}

func (t *checkedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.proxy.For(req.URL) == nil {
		return t.base.RoundTrip(req)
	}
	if err := t.checkDestination(req.Context(), req.URL.Hostname()); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	ctx := context.WithValue(req.Context(), proxyCtxKey{}, true)
	return t.base.RoundTrip(req.WithContext(ctx))
}

func (t *checkedTransport) checkDestination(ctx context.Context, host string) error {
	if t.policy.deny.matchHost(host) {
		return fmt.Errorf("%w: host %s is denied by the administrator", ErrBlocked, host)
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		var lerr error
		if ips, lerr = t.lookup(ctx, host); lerr != nil {
			return lerr
		}
	}
	if len(ips) == 0 {
		return &net.DNSError{Err: "no addresses", Name: host}
	}
	for _, ip := range ips {
		if err := t.policy.check(host, ip); err != nil {
			return err
		}
	}
	return nil
}
