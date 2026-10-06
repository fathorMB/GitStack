package cli

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sniffListener accetta sulla stessa porta TLS (primo byte 0x16) e http in
// chiaro: l'Ingress del test risponde a /api/healthz in https e a
// /downloads/ca.crt in http, come quello vero.
type sniffListener struct {
	net.Listener
	conf *tls.Config
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (l *sniffListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	pc := &peekConn{Conn: c, r: bufio.NewReader(c)}
	b, err := pc.r.Peek(1)
	if err == nil && b[0] == 0x16 {
		return tls.Server(pc, l.conf), nil
	}
	return pc, nil
}

const testPEM = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

// ingress avvia l'Ingress finto: healthz risponde sempre 200, ca.crt dà 502
// per i primi caCrash tentativi (-1 = sempre) e poi 200 con un PEM.
func ingress(t *testing.T, a *App, caCrash int32) (host string, caHits *atomic.Int32) {
	t.Helper()
	caHits = new(atomic.Int32)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/downloads/ca.crt", func(w http.ResponseWriter, _ *http.Request) {
		n := caHits.Add(1)
		if caCrash < 0 || n <= caCrash {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(testPEM))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conf, client := newTLSConfig(t)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(&sniffListener{Listener: ln, conf: conf}) }()
	t.Cleanup(func() { _ = srv.Close() })
	a.HTTP = client
	a.CheckServing = nil
	a.ServingTimeout, a.ServingPoll = 2*time.Second, 10*time.Millisecond
	return ln.Addr().String(), caHits
}

// restoreOn fa un backup e poi un restore con la config data.
func restoreOn(t *testing.T, a *App, cfg, tlsMode, host string) (int, string) {
	t.Helper()
	if err := os.WriteFile(cfg, []byte("version: 1\nhost: "+host+"\ntls: "+tlsMode+"\nimage_tag: sha-aaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Fatalf("backup exit %d", code)
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 1 {
		t.Fatalf("archivi: %v", ents)
	}
	a.Runner = &recRunner{}
	a.Getenv = func(string) string { return "" }
	var stderr strings.Builder
	a.Stderr = &stderr
	code := a.Run(context.Background(), []string{"restore", "--config", cfg, "--dest", dest, ents[0]})
	return code, stderr.String()
}

func TestRestoreWaitsForIngressRetrying(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	host, hits := ingress(t, a, 2) // 502 due volte, poi 200
	code, stderr := restoreOn(t, a, cfg, "internal", host)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if hits.Load() != 3 {
		t.Errorf("tentativi su ca.crt = %d, attesi 3", hits.Load())
	}
}

func TestRestoreIngressTimeoutExits9(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	host, _ := ingress(t, a, -1)
	a.ServingTimeout = 300 * time.Millisecond
	code, stderr := restoreOn(t, a, cfg, "internal", host)
	if code != ExitNotServing || ExitNotServing != 9 {
		t.Fatalf("exit %d, atteso 9; stderr: %s", code, stderr)
	}
	for _, want := range []string{"http://" + host + "/downloads/ca.crt", "HTTP 502", "gitstack status"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr senza %q: %s", want, stderr)
		}
	}
}

func TestRestoreInsecureDoesNotAskForCA(t *testing.T) {
	for _, mode := range []string{"insecure", "letsencrypt"} {
		t.Run(mode, func(t *testing.T) {
			a, cfg := backupApp(t, "sha-aaa")
			host, hits := ingress(t, a, -1) // ca.crt darebbe sempre 502
			code, stderr := restoreOn(t, a, cfg, mode, host)
			if code != ExitOK {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			if hits.Load() != 0 {
				t.Errorf("/downloads/ca.crt chiesto %d volte con tls %s", hits.Load(), mode)
			}
		})
	}
}

// newTLSConfig prende certificato e client fidato da un server TLS di prova
// (poi chiuso: il client conserva la CA).
func newTLSConfig(t *testing.T) (*tls.Config, *http.Client) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	return &tls.Config{Certificates: ts.TLS.Certificates, MinVersion: tls.VersionTLS12}, ts.Client()
}
