// Package mailer manda le email di GitStack via SMTP (M-06/F, GIT-134, C5).
//
// L'SMTP è facoltativo: con Config.Host vuoto Enabled() è false e chi lo usa
// non deve nemmeno provare a inviare. Le email sono solo notifiche: nessun
// Reply-To, `Auto-Submitted: auto-generated`, nessuna risposta per email.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Security è la protezione del canale verso il server SMTP.
type Security string

const (
	// SecurityStartTLS: connessione in chiaro poi STARTTLS (porta 587), default.
	SecurityStartTLS Security = "starttls"
	// SecurityTLS: TLS fin dall'inizio (porta 465).
	SecurityTLS Security = "tls"
	// SecurityNone: nessuna cifratura (porta 25), solo per reti fidate. Le
	// credenziali non si mandano mai senza TLS verso un host non locale.
	SecurityNone Security = "none"
)

// DefaultPort è la porta standard per la protezione scelta.
func DefaultPort(s Security) int {
	switch s {
	case SecurityTLS:
		return 465
	case SecurityNone:
		return 25
	default:
		return 587
	}
}

// Config è la configurazione SMTP di core (variabili GITSTACK_CORE_SMTP_*).
type Config struct {
	Host     string
	Port     int
	Security Security
	Username string
	Password string
	// From è il mittente: «Nome <indirizzo>» o solo l'indirizzo.
	From string
}

// Enabled dice se l'SMTP è configurato: senza host nessuna email parte.
func (c Config) Enabled() bool { return strings.TrimSpace(c.Host) != "" }

// Message è un'email di testo.
type Message struct {
	To      string // indirizzo del destinatario
	Subject string
	Body    string
}

// Mailer invia i messaggi. Un Mailer per Config; sicuro per l'uso concorrente.
type Mailer struct {
	Cfg Config
	// Timeout limita connessione e dialogo SMTP (default 30 s).
	Timeout time.Duration
	// TLSConfig sostituisce la configurazione TLS di default (solo test).
	TLSConfig *tls.Config
	// Now è l'orologio per l'intestazione Date (default time.Now).
	Now func() time.Time
}

// Send invia un messaggio. Un errore è sempre di consegna al server SMTP:
// chi lo chiama decide se ritentare.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if !m.Cfg.Enabled() {
		return errors.New("SMTP non configurato")
	}
	from, err := mail.ParseAddress(m.Cfg.From)
	if err != nil {
		return fmt.Errorf("mittente non valido: %w", err)
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("destinatario non valido: %w", err)
	}
	data, err := m.build(from, to, msg)
	if err != nil {
		return err
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	port := m.Cfg.Port
	if port == 0 {
		port = DefaultPort(m.Cfg.Security)
	}
	host := strings.TrimSpace(m.Cfg.Host)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tcfg := m.TLSConfig
	if tcfg == nil {
		tcfg = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connessione a %s: %w", addr, err)
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	if m.Cfg.Security == SecurityTLS {
		conn = tls.Client(conn, tcfg)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("dialogo SMTP con %s: %w", addr, err)
	}
	defer func() { _ = c.Close() }()

	if m.Cfg.Security == SecurityStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("il server %s non offre STARTTLS", addr)
		}
		if err := c.StartTLS(tcfg); err != nil {
			return fmt.Errorf("STARTTLS con %s: %w", addr, err)
		}
	}
	if m.Cfg.Username != "" {
		// PlainAuth rifiuta le credenziali su un canale non cifrato verso un
		// host che non sia locale.
		if err := c.Auth(smtp.PlainAuth("", m.Cfg.Username, m.Cfg.Password, host)); err != nil {
			return fmt.Errorf("autenticazione SMTP: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return fmt.Errorf("scrittura del messaggio: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("invio del messaggio: %w", err)
	}
	_ = c.Quit()
	return nil
}

func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func (m *Mailer) build(from, to *mail.Address, msg Message) ([]byte, error) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	var id [12]byte
	if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
		return nil, err
	}
	domain := "gitstack.local"
	if _, d, ok := strings.Cut(from.Address, "@"); ok && d != "" {
		domain = d
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", oneLine(msg.Subject)))
	h("Date", now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	// Notifica automatica: nessuna risposta, niente fuori-sede (C5).
	h("Auto-Submitted", "auto-generated")
	h("X-Auto-Response-Suppress", "All")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(msg.Body, "\r\n", "\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
