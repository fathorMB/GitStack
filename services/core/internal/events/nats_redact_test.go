package events

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestNewNATSPublisher_ErroreSenzaPassword: l'URL NATS porta utente e
// password (chart, F8) e l'errore finisce nei log: non deve contenere la
// password (ASVS V7.1.1).
func TestNewNATSPublisher_ErroreSenzaPassword(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, nc, err := NewNATSPublisher(ctx, "nats://u:segreta@127.0.0.1:1")
	if err == nil {
		nc.Close()
		t.Fatal("NewNATSPublisher: voluto un errore con NATS irraggiungibile")
	}
	if strings.Contains(err.Error(), "segreta") {
		t.Errorf("l'errore contiene la password: %v", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("l'errore dovrebbe indicare host e porta: %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	for _, in := range []string{"nats://u:segreta@nats:4222", "nats://segreta@nats:4222", "nats://u:segreta@[::1"} {
		if got := redactURL(in); strings.Contains(got, "segreta") {
			t.Errorf("redactURL(%q) = %q: contiene la password", in, got)
		}
	}
	if got := redactURL("nats://u:p@nats:4222"); got != "nats://nats:4222" {
		t.Errorf("redactURL = %q, voluto nats://nats:4222", got)
	}
}
