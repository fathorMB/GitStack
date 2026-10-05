package pushevent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
)

// NATSPublisher pubblica su NATS JetStream con pkg/events. A differenza di
// core non richiede NATS all'avvio: la connessione si riprova in
// background e lo stream del dominio git si crea alla prima pubblicazione
// possibile, così un NATS giù non ferma né il servizio né i push.
type NATSPublisher struct {
	nc *nats.Conn

	mu  sync.Mutex
	pub *events.Publisher
}

// NewNATSPublisher prepara la connessione a url (non fallisce se NATS non
// risponde). Chiudere con Close.
func NewNATSPublisher(url string) (*NATSPublisher, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Name("gitstack-git"),
	)
	if err != nil {
		return nil, fmt.Errorf("connessione a NATS %q: %w", url, err)
	}
	return &NATSPublisher{nc: nc}, nil
}

// Close chiude la connessione.
func (p *NATSPublisher) Close() { p.nc.Close() }

// ready ritorna il Publisher, creando lo stream alla prima volta.
func (p *NATSPublisher) ready(ctx context.Context) (*events.Publisher, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pub != nil {
		return p.pub, nil
	}
	js, err := jetstream.New(p.nc)
	if err != nil {
		return nil, fmt.Errorf("contesto JetStream: %w", err)
	}
	if _, err := events.EnsureStream(ctx, js, events.Domain(gitpush.Name)); err != nil {
		return nil, err
	}
	p.pub = events.NewPublisher(js)
	return p.pub, nil
}

// Publish implementa Publisher.
func (p *NATSPublisher) Publish(ctx context.Context, name string, version int, payload any) error {
	pub, err := p.ready(ctx)
	if err != nil {
		return err
	}
	_, err = pub.Publish(ctx, name, version, payload)
	return err
}
