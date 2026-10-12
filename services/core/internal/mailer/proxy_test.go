package mailer_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/mailer/mailertest"
)

// N9: con un proxy aziendale la connessione SMTP passa da un CONNECT.
func TestSend_PassaDalProxyAziendale(t *testing.T) {
	srv := mailertest.Start(t, &mailertest.Server{})
	var mu sync.Mutex
	var connects []string
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "solo CONNECT", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		connects = append(connects, r.Host)
		mu.Unlock()
		up, err := net.Dial("tcp", net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		down, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = io.WriteString(down, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
	}))
	defer px.Close()
	pc, err := egress.NewProxyConfig("", px.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	c := mailer.Config{Host: "smtp.corp.example", Port: srv.Port, Security: mailer.SecurityNone, From: "GitStack <noreply@example.com>"}
	if err := (&mailer.Mailer{Cfg: c, Proxy: pc}).Send(context.Background(), msg()); err != nil {
		t.Fatalf("Send via proxy: %v", err)
	}
	if got := srv.Messages(); len(got) != 1 {
		t.Fatalf("messaggi consegnati: %d", len(got))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(connects) != 1 || connects[0] != net.JoinHostPort("smtp.corp.example", strconv.Itoa(srv.Port)) {
		t.Fatalf("CONNECT visti: %v", connects)
	}
	// NO_PROXY: connessione diretta, il proxy non vede altro.
	pc2, _ := egress.NewProxyConfig("", px.URL, srv.Host)
	c2 := mailer.Config{Host: srv.Host, Port: srv.Port, Security: mailer.SecurityNone, From: "GitStack <noreply@example.com>"}
	if err := (&mailer.Mailer{Cfg: c2, Proxy: pc2}).Send(context.Background(), msg()); err != nil {
		t.Fatalf("Send con NO_PROXY: %v", err)
	}
	if len(connects) != 1 {
		t.Fatalf("NO_PROXY non rispettato: %v", connects)
	}
}
