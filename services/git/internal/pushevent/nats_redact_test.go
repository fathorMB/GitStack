package pushevent

import (
	"strings"
	"testing"
)

// TestNewNATSPublisher_ErroreSenzaPassword: l'URL NATS porta utente e
// password (chart, F8) e l'errore finisce nei log: non deve contenerla
// (ASVS V7.1.1).
func TestNewNATSPublisher_ErroreSenzaPassword(t *testing.T) {
	// Con RetryOnFailedConnect un NATS irraggiungibile non è un errore: si
	// prova un URL che nats.go rifiuta subito.
	p, err := NewNATSPublisher("nats://u:segreta@[::1")
	if err == nil {
		p.Close()
		t.Skip("nats.go ha accettato l'URL: nessun errore da ispezionare")
	}
	if strings.Contains(err.Error(), "segreta") {
		t.Errorf("l'errore contiene la password: %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	for _, in := range []string{"nats://u:segreta@127.0.0.1:1", "nats://segreta@nats:4222", "nats://u:segreta@[::1"} {
		if got := redactURL(in); strings.Contains(got, "segreta") {
			t.Errorf("redactURL(%q) = %q: contiene la password", in, got)
		}
	}
	if got := redactURL("nats://u:p@nats:4222"); got != "nats://nats:4222" {
		t.Errorf("redactURL = %q, voluto nats://nats:4222", got)
	}
}
