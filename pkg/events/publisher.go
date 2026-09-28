package events

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// PublishResult riporta la conferma JetStream di una pubblicazione: senza
// errore, il server ha già scritto il messaggio sullo stream indicato al
// numero di sequenza dato (o rilevato un duplicato, vedi Duplicate).
type PublishResult struct {
	Envelope  Envelope
	Stream    string
	Sequence  uint64
	Duplicate bool
}

// Publisher pubblica eventi sul bus con conferma sincrona di JetStream:
// Publish ritorna solo dopo l'ack del server (non solo dopo l'invio sulla
// rete), come richiesto dai criteri di GIT-6.
type Publisher struct {
	js jetstream.JetStream
}

// NewPublisher costruisce un Publisher sopra un jetstream.JetStream già
// connesso (vedi jetstream.New in nats.go). Il chiamante possiede la
// connessione NATS sottostante e ne gestisce il ciclo di vita.
func NewPublisher(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// Publish costruisce l'envelope (nome, versione, id, timestamp, payload) e
// lo pubblica sul subject NATS pari al nome evento (docs/events.md),
// attendendo la conferma di JetStream. Lo stream che copre quel subject
// deve già esistere (vedi EnsureStream): JetStream rifiuta la pubblicazione
// su subject non coperti da nessuno stream.
func (p *Publisher) Publish(ctx context.Context, name string, version int, payload any) (PublishResult, error) {
	env, err := NewEnvelope(name, version, payload)
	if err != nil {
		return PublishResult{}, err
	}
	data, err := env.Marshal()
	if err != nil {
		return PublishResult{}, err
	}
	ack, err := p.js.Publish(ctx, name, data)
	if err != nil {
		return PublishResult{}, fmt.Errorf("events: publish %q v%d su JetStream: %w", name, version, err)
	}
	return PublishResult{
		Envelope:  env,
		Stream:    ack.Stream,
		Sequence:  ack.Sequence,
		Duplicate: ack.Duplicate,
	}, nil
}
