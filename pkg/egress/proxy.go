package egress

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// ProxyConfig è il proxy aziendale di uscita (N9): HTTP_PROXY, HTTPS_PROXY e
// NO_PROXY dal chart. Il proxy non sostituisce la policy: con un proxy
// l'indirizzo dialato è quello del proxy, quindi la destinazione vera si
// giudica PRIMA di aprire la richiesta (o il CONNECT), sull'host dell'URL e su
// tutti gli indirizzi in cui si risolve.
type ProxyConfig struct {
	http, https *url.URL
	noProxy     []string
}

// NewProxyConfig valida i tre valori. Stringhe vuote = non impostato. Un
// valore senza schema vale http://; sono ammessi http, https e socks5.
// Se non c'è nessun proxy, il risultato è nil (nessun proxy).
func NewProxyConfig(httpProxy, httpsProxy, noProxy string) (*ProxyConfig, error) {
	c := &ProxyConfig{}
	var err error
	if c.http, err = parseProxyURL(httpProxy); err != nil {
		return nil, fmt.Errorf("HTTP_PROXY: %w", err)
	}
	if c.https, err = parseProxyURL(httpsProxy); err != nil {
		return nil, fmt.Errorf("HTTPS_PROXY: %w", err)
	}
	for _, e := range strings.Split(noProxy, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			c.noProxy = append(c.noProxy, e)
		}
	}
	if c.http == nil && c.https == nil {
		return nil, nil
	}
	return c, nil
}

// ProxyFromEnv legge HTTP_PROXY, HTTPS_PROXY e NO_PROXY (anche minuscole).
// Nessuna variabile impostata: nil, senza errore.
func ProxyFromEnv() (*ProxyConfig, error) {
	return NewProxyConfig(envAny("HTTP_PROXY", "http_proxy"), envAny("HTTPS_PROXY", "https_proxy"), envAny("NO_PROXY", "no_proxy"))
}

func envAny(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("indirizzo del proxy non valido")
	}
	switch u.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("schema %q non supportato", u.Scheme)
	}
	return u, nil
}

// For dice quale proxy usare per la destinazione u: nil se nessuno (non
// configurato, o u è in NO_PROXY).
func (c *ProxyConfig) For(u *url.URL) *url.URL {
	if c == nil || u == nil {
		return nil
	}
	if c.bypass(u.Hostname(), u.Port(), u.Scheme) {
		return nil
	}
	if u.Scheme == "https" {
		return c.https
	}
	if u.Scheme == "http" {
		return c.http
	}
	return nil
}

func (c *ProxyConfig) bypass(host, port, scheme string) bool {
	host = normalizeHost(host)
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[scheme]
	}
	ip, ipErr := netip.ParseAddr(host)
	for _, e := range c.noProxy {
		if e == "*" {
			return true
		}
		ep := ""
		if h, p, err := net.SplitHostPort(e); err == nil {
			e, ep = h, p
		}
		if ep != "" && ep != port {
			continue
		}
		if pfx, err := netip.ParsePrefix(e); err == nil {
			if ipErr == nil && pfx.Contains(ip.Unmap()) {
				return true
			}
			continue
		}
		if eip, err := netip.ParseAddr(strings.Trim(e, "[]")); err == nil {
			if ipErr == nil && eip.Unmap() == ip.Unmap() {
				return true
			}
			continue
		}
		e = strings.TrimPrefix(strings.TrimPrefix(e, "*"), ".")
		if e != "" && (host == e || strings.HasSuffix(host, "."+e)) {
			return true
		}
	}
	return false
}

// DialContext apre una connessione TCP verso addr (host:porta) attraverso il
// proxy HTTP con CONNECT, se ne vale uno per quella destinazione (HTTPS_PROXY,
// poi HTTP_PROXY, salvo NO_PROXY); altrimenti connette direttamente. Serve ai
// protocolli che non sono HTTP (SMTP). NON applica la policy di uscita: la
// destinazione è quella configurata dall'amministratore, non dagli utenti.
func (c *ProxyConfig) DialContext(ctx context.Context, d *net.Dialer, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var p *url.URL
	if c != nil && !c.bypass(host, port, "https") {
		if p = c.https; p == nil {
			p = c.http
		}
	}
	if p == nil {
		return d.DialContext(ctx, "tcp", addr)
	}
	if p.Scheme != "http" {
		return nil, fmt.Errorf("proxy %s: per i protocolli non HTTP serve un proxy http", p.Scheme)
	}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr(p))
	if err != nil {
		return nil, fmt.Errorf("connessione al proxy: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if p.User != nil {
		pw, _ := p.User.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(p.User.Username()+":"+pw)) + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("CONNECT: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("risposta del proxy: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("il proxy ha rifiutato il CONNECT a %s: %s", addr, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

func proxyAddr(p *url.URL) string {
	port := p.Port()
	if port == "" {
		port = "80"
		if p.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(p.Hostname(), port)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
