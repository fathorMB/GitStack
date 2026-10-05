package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func mustPolicy(t *testing.T, cfg Config) *Policy {
	t.Helper()
	p, err := NewPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckIPCategories(t *testing.T) {
	p := mustPolicy(t, Config{})
	blocked := map[string][]string{
		"loopback":     {"127.0.0.1", "127.255.0.9", "::1", "::ffff:127.0.0.1"},
		"link-local":   {"169.254.169.254", "169.254.0.1", "fe80::1", "fe80::1%eth0", "::ffff:169.254.169.254"},
		"cluster":      {"10.42.0.5", "10.43.12.1", "::ffff:10.42.1.1"},
		"unspecified":  {"0.0.0.0", "0.1.2.3", "::"},
		"multicast":    {"224.0.0.1", "239.1.1.1", "ff02::1", "ff0e::1"},
		"broadcast":    {"255.255.255.255", "240.0.0.1"},
		"special":      {"100.64.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.7", "203.0.113.9", "2001:db8::1", "2002::1", "2001::1"},
		"nat64-nested": {"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe"},
		"compat":       {"::127.0.0.1"},
	}
	for cat, ips := range blocked {
		for _, s := range ips {
			if err := p.CheckIP(netip.MustParseAddr(s)); !errors.Is(err, ErrBlocked) {
				t.Errorf("%s: %s should be blocked, got %v", cat, s, err)
			}
		}
	}
	allowed := []string{"8.8.8.8", "10.0.0.1", "10.41.255.255", "10.44.0.1", "172.16.5.5", "192.168.1.10",
		"2606:4700::1111", "fd12:3456::1", "::ffff:8.8.8.8", "64:ff9b::808:808"}
	for _, s := range allowed {
		if err := p.CheckIP(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s should be allowed (corporate/public), got %v", s, err)
		}
	}
}

func TestClusterCIDRsConfigurable(t *testing.T) {
	p := mustPolicy(t, Config{ClusterCIDRs: []string{"172.20.0.0/14"}})
	if p.CheckIP(netip.MustParseAddr("172.21.0.1")) == nil {
		t.Error("custom cluster CIDR not blocked")
	}
	// Configuring custom CIDRs replaces the defaults.
	if err := p.CheckIP(netip.MustParseAddr("10.42.0.1")); err != nil {
		t.Errorf("default CIDR should no longer apply: %v", err)
	}
	if _, err := NewPolicy(Config{ClusterCIDRs: []string{"example.com"}}); err == nil {
		t.Error("host name accepted as cluster CIDR")
	}
	// Empty list falls back to the k3s defaults, never to "no cluster".
	p = mustPolicy(t, Config{ClusterCIDRs: []string{}})
	if p.CheckIP(netip.MustParseAddr("10.43.0.1")) == nil {
		t.Error("empty ClusterCIDRs must keep defaults")
	}
}

func TestAdminListsDenyAndAllow(t *testing.T) {
	ip := netip.MustParseAddr
	// deny narrows inside the corporate network
	p := mustPolicy(t, Config{Deny: []string{"10.9.0.0/16", "192.168.5.5"}})
	if p.CheckIP(ip("10.9.1.1")) == nil || p.CheckIP(ip("192.168.5.5")) == nil {
		t.Error("deny entries not applied")
	}
	if p.CheckIP(ip("10.8.1.1")) != nil {
		t.Error("deny too broad")
	}
	// allow restricts: only listed destinations pass
	p = mustPolicy(t, Config{Allow: []string{"10.0.0.0/8"}})
	if p.CheckIP(ip("10.1.1.1")) != nil {
		t.Error("allowed CIDR refused")
	}
	if p.CheckIP(ip("8.8.8.8")) == nil {
		t.Error("allow list must restrict")
	}
	// allow widens special ranges (CGNAT) ...
	p = mustPolicy(t, Config{Allow: []string{"100.64.0.0/10"}})
	if p.CheckIP(ip("100.100.1.1")) != nil {
		t.Error("allow must unlock CGNAT")
	}
	// ... but never loopback, link-local, cluster, multicast, unspecified
	p = mustPolicy(t, Config{Allow: []string{
		"127.0.0.0/8", "::1", "169.254.0.0/16", "fe80::/10", "10.42.0.0/16", "10.43.0.0/16", "0.0.0.0/0", "::/0", "224.0.0.0/4",
	}})
	for _, s := range []string{"127.0.0.1", "::1", "169.254.169.254", "fe80::1", "10.42.0.1", "10.43.0.1", "224.0.0.1", "0.0.0.0"} {
		if p.CheckIP(ip(s)) == nil {
			t.Errorf("allow unlocked %s", s)
		}
	}
	// deny wins over allow
	p = mustPolicy(t, Config{Allow: []string{"10.0.0.0/8"}, Deny: []string{"10.1.0.0/16"}})
	if p.CheckIP(ip("10.1.2.3")) == nil || p.CheckIP(ip("10.2.2.3")) != nil {
		t.Error("deny must win over allow")
	}
	for _, bad := range []string{"10.0.0.0/33", "a*b", "*.", "host name"} {
		if _, err := NewPolicy(Config{Allow: []string{bad}}); err == nil {
			t.Errorf("entry %q should be invalid", bad)
		}
	}
}

func TestHostLists(t *testing.T) {
	p := mustPolicy(t, Config{Allow: []string{"hooks.corp.example", "*.ci.example"}, Deny: []string{"bad.corp.example"}})
	ip := netip.MustParseAddr("10.1.1.1")
	if p.check("hooks.corp.example", ip) != nil || p.check("X.CI.example.", ip) != nil {
		t.Error("allowed host refused")
	}
	if p.check("other.example", ip) == nil {
		t.Error("host outside allow list accepted")
	}
	if p.check("ci.example", ip) == nil {
		t.Error("suffix pattern must not match the bare domain")
	}
	p = mustPolicy(t, Config{Deny: []string{"bad.corp.example"}})
	if p.check("bad.corp.example", ip) == nil {
		t.Error("denied host accepted")
	}
	// a host allow never unlocks a loopback/cluster address
	p = mustPolicy(t, Config{Allow: []string{"hooks.corp.example"}})
	if p.check("hooks.corp.example", netip.MustParseAddr("127.0.0.1")) == nil {
		t.Error("host allow unlocked loopback")
	}
}

// --- client ---

func newBackend(t *testing.T, h http.Handler) (srv *httptest.Server, addr string) {
	t.Helper()
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, srv.Listener.Addr().String()
}

// testClient resolves names through table and, for permitted addresses, lets
// the test connect to backend instead of the (fictional) IP.
func testClient(t *testing.T, p *Policy, table map[string][]string, backend string) *http.Client {
	t.Helper()
	return NewClient(p, Options{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, s := range table[host] {
				out = append(out, netip.MustParseAddr(s))
			}
			if out == nil {
				return nil, &net.DNSError{Err: "no such host", Name: host}
			}
			return out, nil
		},
		connect: func(ctx context.Context, _ *net.Dialer, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, backend)
		},
	})
}

func TestRealDialerBlocksLoopback(t *testing.T) {
	srv, _ := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }))
	c := NewClient(mustPolicy(t, Config{}), Options{})
	_, err := c.Get(srv.URL)
	if !IsBlocked(err) {
		t.Fatalf("httptest on 127.0.0.1 must be blocked, got %v", err)
	}
	// a literal allow of loopback does not help
	c = NewClient(mustPolicy(t, Config{Allow: []string{"127.0.0.1"}}), Options{})
	if _, err = c.Get(srv.URL); !IsBlocked(err) {
		t.Fatalf("allow must not unlock loopback, got %v", err)
	}
	for _, u := range []string{"http://[::1]:1/", "http://[fe80::1]:1/", "http://169.254.169.254/latest/meta-data", "http://[::ffff:127.0.0.1]:1/", "http://0.0.0.0:1/", "http://10.42.0.1/"} {
		if _, err := c.Get(u); !IsBlocked(err) {
			t.Errorf("%s: want blocked, got %v", u, err)
		}
	}
}

// The Control hook alone (no pre-check in DialContext) refuses loopback, so
// rebinding between lookup and connect is covered.
func TestControlBlocksAtConnect(t *testing.T) {
	srv, _ := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	p := mustPolicy(t, Config{})
	c := NewClient(p, Options{
		// lookup lies: the checked (public) address differs from the one connected.
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		connect: func(ctx context.Context, d *net.Dialer, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, srv.Listener.Addr().String()) // real dialer with Control
		},
	})
	if _, err := c.Get("http://rebind.example/"); !IsBlocked(err) {
		t.Fatalf("want blocked by Control, got %v", err)
	}
}

func TestDNSResolvingToBlockedAddress(t *testing.T) {
	srv, addr := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	_ = srv
	table := map[string][]string{
		"evil.example":   {"127.0.0.1"},
		"meta.example":   {"169.254.169.254"},
		"v6.example":     {"::1"},
		"ll6.example":    {"fe80::1"},
		"mapped.example": {"::ffff:127.0.0.1"},
		"pod.example":    {"10.42.3.4"},
		"mixed.example":  {"127.0.0.1", "10.1.2.3"}, // blocked one skipped, good one used
		"ok.example":     {"10.1.2.3"},
	}
	c := testClient(t, mustPolicy(t, Config{}), table, addr)
	for _, h := range []string{"evil", "meta", "v6", "ll6", "mapped", "pod"} {
		if _, err := c.Get("http://" + h + ".example/"); !IsBlocked(err) {
			t.Errorf("%s: want blocked, got %v", h, err)
		}
	}
	for _, h := range []string{"ok", "mixed"} {
		resp, err := c.Get("http://" + h + ".example/")
		if err != nil {
			t.Fatalf("%s: %v", h, err)
		}
		_ = resp.Body.Close()
	}
	if _, err := c.Get("http://nxdomain.example/"); err == nil || IsBlocked(err) {
		t.Errorf("NXDOMAIN should be a plain DNS error, got %v", err)
	}
}

func TestDeniedHostNotResolved(t *testing.T) {
	called := false
	c := NewClient(mustPolicy(t, Config{Deny: []string{"*.blocked.example"}}), Options{
		lookup: func(context.Context, string) ([]netip.Addr, error) { called = true; return nil, nil },
	})
	if _, err := c.Get("http://a.blocked.example/"); !IsBlocked(err) || called {
		t.Fatalf("denied host: err=%v lookup called=%v", err, called)
	}
}

func TestRedirects(t *testing.T) {
	var hits int
	_, addr := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/to-loopback":
			http.Redirect(w, r, "http://127.0.0.1:1/x", http.StatusFound)
		case "/to-metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusMovedPermanently)
		case "/to-v6":
			http.Redirect(w, r, "http://[::1]:1/", http.StatusTemporaryRedirect)
		case "/to-rebind":
			http.Redirect(w, r, "http://evil.example/", http.StatusFound)
		case "/to-file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/to-ok":
			http.Redirect(w, r, "http://other.example/done", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "done")
		}
	}))
	table := map[string][]string{"good.example": {"10.1.2.3"}, "other.example": {"192.168.1.2"}, "evil.example": {"127.0.0.1"}}
	c := testClient(t, mustPolicy(t, Config{}), table, addr)

	for _, p := range []string{"/to-loopback", "/to-metadata", "/to-v6", "/to-rebind", "/to-file"} {
		resp, err := c.Get("http://good.example" + p)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !IsBlocked(err) {
			t.Errorf("%s: want blocked, got %v", p, err)
		}
	}
	resp, err := c.Get("http://good.example/to-ok")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "done" {
		t.Errorf("body %q", b)
	}
	hits = 0
	if _, err := c.Get("http://good.example/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("loop must stop, got %v", err)
	}
	if hits != DefaultMaxRedirects+1 {
		t.Errorf("hits = %d, want %d", hits, DefaultMaxRedirects+1)
	}
}

func TestNoRedirects(t *testing.T) {
	_, addr := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/x", http.StatusFound)
	}))
	c := NewClient(mustPolicy(t, Config{}), Options{
		MaxRedirects: -1,
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.1.1.1")}, nil
		},
		connect: func(ctx context.Context, _ *net.Dialer, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	})
	if _, err := c.Get("http://h.example/"); err == nil {
		t.Error("redirect followed with MaxRedirects<0")
	}
}

func TestReadBodyTruncates(t *testing.T) {
	long := strings.Repeat("a", MaxLoggedBody+10)
	b, trunc, err := ReadBody(strings.NewReader(long))
	if err != nil || !trunc || len(b) != MaxLoggedBody {
		t.Errorf("len=%d trunc=%v err=%v", len(b), trunc, err)
	}
	b, trunc, _ = ReadBody(strings.NewReader(long[:MaxLoggedBody]))
	if trunc || len(b) != MaxLoggedBody {
		t.Errorf("exact size: len=%d trunc=%v", len(b), trunc)
	}
	b, trunc, _ = ReadBody(strings.NewReader("hi"))
	if trunc || string(b) != "hi" {
		t.Errorf("short body: %q %v", b, trunc)
	}
	if b, trunc, err = ReadBody(nil); b != nil || trunc || err != nil {
		t.Error("nil reader")
	}
}

func TestControlForRealPath(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		host    string
		addr    string
		blocked bool
	}{
		{"allow host ok", Config{Allow: []string{"hooks.corp.example"}}, "hooks.corp.example", "10.1.2.3:443", false},
		{"allow host other", Config{Allow: []string{"hooks.corp.example"}}, "other.example", "10.1.2.3:443", true},
		{"allow suffix ok", Config{Allow: []string{"*.ci.example"}}, "x.ci.example", "10.1.2.3:443", false},
		{"allowed name to loopback", Config{Allow: []string{"hooks.corp.example"}}, "hooks.corp.example", "127.0.0.1:80", true},
		{"allowed name to cluster", Config{Allow: []string{"hooks.corp.example"}}, "hooks.corp.example", "10.42.0.1:80", true},
		{"allowed name to special", Config{Allow: []string{"hooks.corp.example"}}, "hooks.corp.example", "100.64.1.1:80", true},
		{"allow cidr unlocks cgnat", Config{Allow: []string{"100.64.0.0/10"}}, "h.example", "100.64.1.1:443", false},
		{"deny ip with allowed name", Config{Allow: []string{"hooks.corp.example"}, Deny: []string{"10.1.0.0/16"}}, "hooks.corp.example", "10.1.2.3:443", true},
		{"ipv6 literal", Config{}, "h.example", "[fe80::1]:80", true},
	}
	for _, tc := range cases {
		err := mustPolicy(t, tc.cfg).controlFor(tc.host)("tcp", tc.addr, nil)
		if IsBlocked(err) != tc.blocked || (!tc.blocked && err != nil) {
			t.Errorf("%s: blocked=%v err=%v", tc.name, tc.blocked, err)
		}
	}
	if err := mustPolicy(t, Config{}).controlFor("h")("udp", "10.1.2.3:53", nil); !IsBlocked(err) {
		t.Errorf("udp: %v", err)
	}
}

// End to end with the real dialer: an allowed host name reaches a real listener.
func TestAllowByNameRealConnect(t *testing.T) {
	_, addr := newBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	// Only the lookup is fake; the backend is on loopback, so the real Control
	// must refuse it even though the name is allowed.
	c := NewClient(mustPolicy(t, Config{Allow: []string{"hooks.corp.example"}}), Options{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
		},
		connect: func(ctx context.Context, d *net.Dialer, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	})
	if _, err := c.Get("http://hooks.corp.example/"); !IsBlocked(err) {
		t.Fatalf("loopback must stay blocked for an allowed name: %v", err)
	}
}
