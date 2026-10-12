package egress

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// publicLookup finge che ogni nome si risolva in un indirizzo pubblico, o in
// quello dato per nome.
func lookupMap(m map[string]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		if a, ok := m[host]; ok {
			return []netip.Addr{netip.MustParseAddr(a)}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
}

// proxyRecorder è un proxy HTTP finto su 127.0.0.1 (indirizzo che la policy
// bloccherebbe se fosse una destinazione) che conta le richieste.
type proxyRecorder struct {
	mu    sync.Mutex
	seen  []string
	auth  []string
	conns atomic.Int64
	srv   *httptest.Server
}

func newProxyRecorder(t *testing.T) *proxyRecorder {
	t.Helper()
	p := &proxyRecorder{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.URL.String())
		p.auth = append(p.auth, r.Header.Get("Proxy-Authorization"))
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	p.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			p.conns.Add(1)
		}
	}
	t.Cleanup(p.srv.Close)
	return p
}

func (p *proxyRecorder) config(t *testing.T, noProxy string) *ProxyConfig {
	t.Helper()
	c, err := NewProxyConfig(p.srv.URL, p.srv.URL, noProxy)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newProxied(t *testing.T, cfg Config, pc *ProxyConfig) *http.Client {
	t.Helper()
	pol, err := NewPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(pol, Options{Proxy: pc, lookup: lookupMap(map[string]string{
		"internal.example.com": "10.43.0.5",
		"loop.example.com":     "127.0.0.1",
		"meta.example.com":     "169.254.169.254",
	})})
}

func TestProxy_DestinazioneAmmessaPassaDalProxy(t *testing.T) {
	p := newProxyRecorder(t)
	c := newProxied(t, Config{}, p.config(t, ""))
	resp, err := c.Post("http://hooks.example.com/hook", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) != 1 || p.seen[0] != "POST http://hooks.example.com/hook" {
		t.Fatalf("il proxy ha visto %v", p.seen)
	}
}

func TestProxy_DestinazioneVietataNonContattaIlProxy(t *testing.T) {
	cases := map[string]struct {
		cfg Config
		url string
	}{
		"IP letterale di loopback":      {Config{}, "http://127.0.0.1:8080/x"},
		"nome che risolve in loopback":  {Config{}, "http://loop.example.com/x"},
		"nome che risolve nel cluster":  {Config{}, "http://internal.example.com/x"},
		"metadati cloud (link-local)":   {Config{}, "http://meta.example.com/x"},
		"IP del cluster":                {Config{}, "http://10.42.1.1/x"},
		"nome in deny":                  {Config{Deny: []string{"hooks.example.com"}}, "http://hooks.example.com/x"},
		"fuori dall'allow dell'admin":   {Config{Allow: []string{"ok.example.com"}}, "http://hooks.example.com/x"},
		"IPv4-mapped di loopback":       {Config{}, "http://[::ffff:127.0.0.1]/x"},
		"cluster anche se in allow/0/0": {Config{Allow: []string{"0.0.0.0/0"}}, "http://10.43.0.1/x"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := newProxyRecorder(t)
			c := newProxied(t, tc.cfg, p.config(t, ""))
			resp, err := c.Get(tc.url)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("la richiesta doveva essere rifiutata")
			}
			if !IsBlocked(err) {
				t.Fatalf("errore %v: atteso un blocco di policy", err)
			}
			if n := p.conns.Load(); n != 0 {
				t.Fatalf("il proxy è stato contattato %d volte", n)
			}
		})
	}
}

func TestProxy_RedirectVersoDestinazioneInternaRifiutato(t *testing.T) {
	// Il proxy risponde 302 verso un indirizzo del cluster: il secondo hop si
	// giudica come il primo e non arriva al proxy.
	var hits atomic.Int64
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://10.43.0.9/secret", http.StatusFound)
	}))
	defer redirecting.Close()
	pc, err := NewProxyConfig(redirecting.URL, redirecting.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	c := newProxied(t, Config{}, pc)
	_, err = c.Get("http://hooks.example.com/x")
	if !IsBlocked(err) {
		t.Fatalf("errore %v: atteso un blocco al redirect", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("il proxy ha ricevuto %d richieste, attesa 1", hits.Load())
	}
}

func TestProxy_NoProxyVaDirettoESiGiudicaAlDial(t *testing.T) {
	p := newProxyRecorder(t)
	c := newProxied(t, Config{}, p.config(t, "internal.example.com, .corp.example"))
	// In NO_PROXY e interno: nessun proxy, e il dialer lo blocca.
	_, err := c.Get("http://internal.example.com/x")
	if !IsBlocked(err) {
		t.Fatalf("errore %v: atteso un blocco", err)
	}
	if n := p.conns.Load(); n != 0 {
		t.Fatalf("il proxy è stato contattato %d volte", n)
	}
}

func TestProxy_ConnectPerHttpsEPolicyPrima(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	lines := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(c)
			line, _ := br.ReadString('\n')
			lines <- strings.TrimSpace(line)
			_ = c.Close()
		}
	}()
	pc, err := NewProxyConfig("", "http://user:pw@"+ln.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	c := newProxied(t, Config{}, pc)
	if _, err := c.Get("https://hooks.example.com/x"); err == nil {
		t.Fatal("il proxy chiude: atteso un errore di rete")
	}
	select {
	case l := <-lines:
		if l != "CONNECT hooks.example.com:443 HTTP/1.1" {
			t.Fatalf("prima riga: %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nessun CONNECT ricevuto")
	}
	// Una destinazione interna non arriva al CONNECT.
	_, err = c.Get("https://loop.example.com/x")
	if !IsBlocked(err) {
		t.Fatalf("errore %v: atteso un blocco", err)
	}
	select {
	case l := <-lines:
		t.Fatalf("CONNECT inatteso: %q", l)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProxyConfig_NoProxy(t *testing.T) {
	pc, err := NewProxyConfig("proxy:3128", "proxy:3128", "localhost,.svc,10.0.0.0/8,db.example.com:5432,*.corp")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"http://example.com/":        true, // via proxy
		"https://a.svc/":             false,
		"https://x.corp/":            false,
		"http://10.1.2.3/":           false,
		"http://db.example.com:5432": false,
		"http://db.example.com:80":   true,
		"http://localhost/":          false,
	}
	for raw, viaProxy := range cases {
		u, _ := url.Parse(raw)
		if got := pc.For(u) != nil; got != viaProxy {
			t.Errorf("%s: via proxy = %v, atteso %v", raw, got, viaProxy)
		}
	}
	if pc.For(&url.URL{Scheme: "ftp", Host: "x"}) != nil {
		t.Error("schema non http: nessun proxy")
	}
}

func TestNewProxyConfig_Validazione(t *testing.T) {
	if c, err := NewProxyConfig("", "", "a"); c != nil || err != nil {
		t.Fatalf("nessun proxy: %v %v", c, err)
	}
	if _, err := NewProxyConfig("ftp://p:1", "", ""); err == nil {
		t.Fatal("schema ftp: atteso errore")
	}
	if _, err := NewProxyConfig("http://", "", ""); err == nil {
		t.Fatal("indirizzo vuoto: atteso errore")
	}
}

func TestProxyConfig_DialContextCONNECT(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		br := bufio.NewReader(c)
		var head []string
		for {
			l, err := br.ReadString('\n')
			if err != nil || strings.TrimSpace(l) == "" {
				break
			}
			head = append(head, strings.TrimSpace(l))
		}
		got <- strings.Join(head, "|")
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n220 smtp.example ESMTP\r\n")
	}()
	pc, err := NewProxyConfig("", "u:p@"+ln.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pc.DialContext(context.Background(), &net.Dialer{Timeout: 3 * time.Second}, "smtp.example.com:587")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "220 smtp.example") {
		t.Fatalf("banner %q, %v", line, err)
	}
	head := <-got
	if !strings.HasPrefix(head, "CONNECT smtp.example.com:587 HTTP/1.1|Host: smtp.example.com:587|Proxy-Authorization: Basic ") {
		t.Fatalf("intestazione: %q", head)
	}
}

func TestProxyConfig_DialContextRifiutoEDiretto(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Si legge la richiesta prima di rispondere: chiudere con dati non letti
		// manda un RST che il client può vedere al posto della risposta.
		br := bufio.NewReader(c)
		for {
			l, err := br.ReadString('\n')
			if err != nil || strings.TrimSpace(l) == "" {
				break
			}
		}
		_, _ = io.WriteString(c, "HTTP/1.1 403 Forbidden\r\n\r\n")
		_ = c.Close()
	}()
	pc, _ := NewProxyConfig("", ln.Addr().String(), "")
	if _, err := pc.DialContext(context.Background(), &net.Dialer{Timeout: time.Second}, "smtp.example.com:25"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("atteso rifiuto 403, ottenuto %v", err)
	}
	// Senza proxy (nil) connette direttamente.
	var none *ProxyConfig
	direct, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Close() }()
	conn, err := none.DialContext(context.Background(), &net.Dialer{Timeout: time.Second}, direct.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}
