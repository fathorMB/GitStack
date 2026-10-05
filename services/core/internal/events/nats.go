package events

import (
	"context"
	"fmt"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/testevent"
	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSPublisher adatta pkg/events.Publisher (libreria condivisa di GIT-6)
// all'interfaccia Publisher usata da core: publish con conferma sincrona di
// JetStream, envelope nome/versione/id/timestamp/payload.
type NATSPublisher struct {
	pub *pkgevents.Publisher
	js  jetstream.JetStream
}

var _ Publisher = (*NATSPublisher)(nil)

// NewNATSPublisher si connette a NATS su url, apre il contesto JetStream e
// assicura lo stream del dominio "core" (EnsureStream: JetStream rifiuta la
// pubblicazione su subject non coperti da nessuno stream), prima che core
// riceva la prima richiesta di creazione. Il dominio è derivato dal nome
// dell'evento di prova (pkg/events/testevent), non hard-coded: se in futuro
// core pubblica anche altri eventi dello stesso dominio "core", useranno lo
// stesso stream senza bisogno di un'altra EnsureStream.
//
// Il chiamante possiede la connessione NATS restituita e la chiude quando
// non serve più (vedi main.go, defer nc.Close() nello shutdown di core).
func NewNATSPublisher(ctx context.Context, url string) (*NATSPublisher, *nats.Conn, error) {
	// Reconnessione senza limite: un NATS momentaneamente irraggiungibile
	// non deve spegnere per sempre la pubblicazione (l'outbox ritenta).
	nc, err := nats.Connect(url, nats.MaxReconnects(-1))
	if err != nil {
		return nil, nil, fmt.Errorf("connessione a NATS %q non riuscita: %w", url, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("apertura del contesto JetStream non riuscita: %w", err)
	}

	domain := pkgevents.Domain(testevent.Name)
	if _, err := pkgevents.EnsureStream(ctx, js, domain); err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("creazione/aggiornamento dello stream JetStream per il dominio %q non riuscita: %w", domain, err)
	}

	// Stream dei domini di core (M-06/B, GIT-130): issue, issue_comment,
	// repository. JetStream rifiuta i subject non coperti da uno stream.
	for _, d := range domainevents.Domains {
		if _, err := pkgevents.EnsureStream(ctx, js, d); err != nil {
			nc.Close()
			return nil, nil, fmt.Errorf("creazione/aggiornamento dello stream JetStream per il dominio %q non riuscita: %w", d, err)
		}
	}

	return &NATSPublisher{pub: pkgevents.NewPublisher(js), js: js}, nc, nil
}

// Publish pubblica l'evento su JetStream con conferma sincrona (vedi
// pkg/events.Publisher.Publish) e scarta il risultato di conferma
// (stream/sequenza/duplicato): a core, che pubblica soltanto, basta sapere
// se l'operazione è riuscita o no.
func (p *NATSPublisher) Publish(ctx context.Context, name string, version int, payload any) error {
	_, err := p.pub.Publish(ctx, name, version, payload)
	return err
}

// PublishEnvelope pubblica una busta già costruita (id e orario fissati
// dall'outbox) con conferma sincrona di JetStream. L'id della busta è anche
// il Nats-Msg-Id: nella finestra di deduplicazione dello stream un doppio
// invio non crea un secondo messaggio; oltre, i consumatori deduplicano
// sull'id (docs/events.md).
func (p *NATSPublisher) PublishEnvelope(ctx context.Context, env pkgevents.Envelope) error {
	data, err := env.Marshal()
	if err != nil {
		return err
	}
	if _, err := p.js.Publish(ctx, env.Name, data, jetstream.WithMsgID(env.ID)); err != nil {
		return fmt.Errorf("publish %q (id %s) su JetStream: %w", env.Name, env.ID, err)
	}
	return nil
}
