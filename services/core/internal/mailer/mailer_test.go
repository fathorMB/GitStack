package mailer_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/mailer/mailertest"
)

func msg() mailer.Message {
	return mailer.Message{To: "Bea <bea@example.com>", Subject: "[alice/web] Titolo con àccenti\r\nBcc: x@y.z (#4)", Body: "Ciao è un test\nsecondo rigo\n"}
}

func cfg(srv *mailertest.Server, sec mailer.Security) mailer.Config {
	return mailer.Config{Host: srv.Host, Port: srv.Port, Security: sec, From: "GitStack <noreply@example.com>"}
}

func TestSend_SenzaCifraturaConAutenticazione(t *testing.T) {
	srv := mailertest.Start(t, &mailertest.Server{Auth: "robot:segreto"})
	c := cfg(srv, mailer.SecurityNone)
	c.Username, c.Password = "robot", "segreto"
	if err := (&mailer.Mailer{Cfg: c}).Send(context.Background(), msg()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.Messages()
	if len(got) != 1 || got[0].User != "robot" || got[0].From != "noreply@example.com" || len(got[0].To) != 1 || got[0].To[0] != "bea@example.com" {
		t.Fatalf("messaggi: %+v", got)
	}
	d := got[0].Data
	for _, want := range []string{"Auto-Submitted: auto-generated", "MIME-Version: 1.0", "To: ", "Subject: "} {
		if !strings.Contains(d, want) {
			t.Errorf("manca %q in:\n%s", want, d)
		}
	}
	if strings.Contains(strings.ToLower(d), "reply-to") {
		t.Error("nessuna risposta via email: Reply-To non ammesso")
	}
	// Un a-capo nell'oggetto non può iniettare un'intestazione.
	if strings.Contains(d, "\r\nBcc:") {
		t.Errorf("intestazione iniettata:\n%s", d)
	}
	var subj string
	for _, l := range strings.Split(d, "\r\n") {
		if v, ok := strings.CutPrefix(l, "Subject: "); ok {
			subj, _ = new(mime.WordDecoder).DecodeHeader(v)
		}
	}
	if !strings.Contains(subj, "àccenti") {
		t.Errorf("oggetto: %q", subj)
	}
}

func TestSend_CredenzialiErrate(t *testing.T) {
	srv := mailertest.Start(t, &mailertest.Server{Auth: "robot:segreto"})
	c := cfg(srv, mailer.SecurityNone)
	c.Username, c.Password = "robot", "sbagliata"
	if err := (&mailer.Mailer{Cfg: c}).Send(context.Background(), msg()); err == nil {
		t.Fatal("attesi errore di autenticazione")
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("nessuna email deve partire")
	}
}

func tlsPair(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.NotFoundHandler())
	ts.StartTLS()
	t.Cleanup(ts.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return &tls.Config{Certificates: ts.TLS.Certificates, MinVersion: tls.VersionTLS12}, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
}

func TestSend_TLSeSTARTTLS(t *testing.T) {
	serverTLS, clientTLS := tlsPair(t)
	for _, sec := range []mailer.Security{mailer.SecurityTLS, mailer.SecurityStartTLS} {
		t.Run(string(sec), func(t *testing.T) {
			srv := mailertest.Start(t, &mailertest.Server{TLS: serverTLS, Implicit: sec == mailer.SecurityTLS, Auth: "u:p"})
			c := cfg(srv, sec)
			c.Username, c.Password = "u", "p"
			if err := (&mailer.Mailer{Cfg: c, TLSConfig: clientTLS}).Send(context.Background(), msg()); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if len(srv.Messages()) != 1 {
				t.Fatalf("messaggi: %d", len(srv.Messages()))
			}
		})
	}
}

func TestSend_StartTLSObbligatorioSeRichiesto(t *testing.T) {
	srv := mailertest.Start(t, &mailertest.Server{}) // niente STARTTLS offerto
	err := (&mailer.Mailer{Cfg: cfg(srv, mailer.SecurityStartTLS)}).Send(context.Background(), msg())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("atteso rifiuto senza STARTTLS, ottenuto %v", err)
	}
}

func TestSend_ServerChiudeInErrore(t *testing.T) {
	srv := mailertest.Start(t, &mailertest.Server{})
	srv.FailNext(1)
	m := &mailer.Mailer{Cfg: cfg(srv, mailer.SecurityNone)}
	if err := m.Send(context.Background(), msg()); err == nil {
		t.Fatal("atteso errore 451")
	}
	if err := m.Send(context.Background(), msg()); err != nil {
		t.Fatalf("la seconda deve riuscire: %v", err)
	}
}

func TestEnabled_SenzaHostNoEmail(t *testing.T) {
	if (mailer.Config{}).Enabled() {
		t.Fatal("senza host l'SMTP è spento")
	}
	if err := (&mailer.Mailer{}).Send(context.Background(), msg()); err == nil {
		t.Fatal("Send senza configurazione deve rifiutare")
	}
}
