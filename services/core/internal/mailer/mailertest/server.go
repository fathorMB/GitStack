// Package mailertest è un server SMTP finto, in-process, per i test (M-06/F,
// GIT-134): ascolta su 127.0.0.1, risponde al dialogo minimo (EHLO, STARTTLS,
// AUTH PLAIN, MAIL, RCPT, DATA, QUIT) e conserva i messaggi ricevuti. Più
// stabile di un container mailpit: nessun Docker.
package mailertest

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Received è un messaggio consegnato al server.
type Received struct {
	From string
	To   []string
	Data string // intestazioni e corpo, come ricevuti (corpo quoted-printable)
	User string // utente autenticato, se c'è stato AUTH
}

// Server è il server SMTP finto.
type Server struct {
	Host string
	Port int

	// TLS: certificato per STARTTLS o per il TLS implicito (nil = nessuno).
	TLS *tls.Config
	// Implicit: TLS fin dalla connessione (porta 465), altrimenti STARTTLS
	// quando TLS è impostato.
	Implicit bool
	// Auth, se non vuoto: "utente:password" richiesti con AUTH PLAIN.
	Auth string

	ln net.Listener
	mu sync.Mutex
	// fail: quante DATA rifiutare ancora con 451 (-1 = tutte).
	fail     int
	msgs     []Received
	attempts int
	wg       sync.WaitGroup
}

// Start avvia il server e lo ferma a fine test.
func Start(t testing.TB, s *Server) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if s.Implicit && s.TLS != nil {
		ln = tls.NewListener(ln, s.TLS)
	}
	s.ln = ln
	addr := ln.Addr().(*net.TCPAddr)
	s.Host, s.Port = "127.0.0.1", addr.Port
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serve(c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); s.wg.Wait() })
	return s
}

// FailNext rifiuta le prossime n consegne (DATA) con 451; n < 0 le rifiuta tutte.
func (s *Server) FailNext(n int) {
	s.mu.Lock()
	s.fail = n
	s.mu.Unlock()
}

// Messages ritorna una copia dei messaggi consegnati.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.msgs...)
}

// Attempts è il numero di DATA ricevuti, rifiutati o no.
func (s *Server) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *Server) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	w := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	w("220 mailertest ESMTP")
	var cur Received
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
			ext := []string{"250-mailertest"}
			if s.TLS != nil && !s.Implicit {
				if _, isTLS := c.(*tls.Conn); !isTLS {
					ext = append(ext, "250-STARTTLS")
				}
			}
			if s.Auth != "" {
				ext = append(ext, "250-AUTH PLAIN")
			}
			ext = append(ext, "250 8BITMIME")
			for _, l := range ext {
				w(l)
			}
		case verb == "STARTTLS":
			w("220 pronto")
			tc := tls.Server(c, s.TLS)
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			r = bufio.NewReader(tc)
			w = func(line string) { _, _ = tc.Write([]byte(line + "\r\n")) }
		case strings.HasPrefix(verb, "AUTH PLAIN"):
			parts := strings.Fields(line)
			raw, _ := base64.StdEncoding.DecodeString(parts[len(parts)-1])
			f := strings.Split(string(raw), "\x00")
			if len(f) == 3 && f[1]+":"+f[2] == s.Auth {
				cur.User = f[1]
				w("235 ok")
			} else {
				w("535 credenziali errate")
			}
		case strings.HasPrefix(verb, "MAIL FROM:"):
			if s.Auth != "" && cur.User == "" {
				w("530 serve l'autenticazione")
				continue
			}
			cur.From = angle(line[len("MAIL FROM:"):])
			cur.To = nil
			w("250 ok")
		case strings.HasPrefix(verb, "RCPT TO:"):
			cur.To = append(cur.To, angle(line[len("RCPT TO:"):]))
			w("250 ok")
		case verb == "DATA":
			w("354 scrivi")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			s.mu.Lock()
			s.attempts++
			reject := s.fail != 0
			if s.fail > 0 {
				s.fail--
			}
			if !reject {
				m := cur
				m.Data = b.String()
				s.msgs = append(s.msgs, m)
			}
			s.mu.Unlock()
			if reject {
				w("451 riprova piu' tardi")
			} else {
				w("250 messaggio accettato")
			}
		case verb == "QUIT":
			w("221 ciao")
			return
		case verb == "RSET", verb == "NOOP":
			w("250 ok")
		default:
			w("502 comando non supportato " + strconv.Quote(line))
		}
	}
}

// angle estrae l'indirizzo da `<indirizzo> PARAMETRI`.
func angle(s string) string {
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return strings.TrimSpace(s)
}
