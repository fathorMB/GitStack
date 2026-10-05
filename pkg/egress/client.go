package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
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
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control runs after resolution, right before connect(2): this is
		// the check that cannot be bypassed by DNS rebinding.
		Control: func(network, address string, _ syscall.RawConn) error {
			switch network {
			case "tcp", "tcp4", "tcp6":
			default:
				return fmt.Errorf("%w: network %q", ErrBlocked, network)
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: unparsable address %q", ErrBlocked, address)
			}
			return policy.check("", ap.Addr())
		},
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if policy.deny.matchHost(host) {
			return nil, fmt.Errorf("%w: host %s is denied by the administrator", ErrBlocked, host)
		}
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
		Proxy:                 nil, // never via an environment proxy: it would be the one dialed
		DialContext:           dialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: opts.Timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
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
