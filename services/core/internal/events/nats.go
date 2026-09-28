package events

import (
	"context"
	"fmt"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/testevent"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSPublisher adatta pkg/events.Publisher (libreria condivisa di GIT-6)
// all'interfaccia Publisher usata da core: publish con conferma sincrona di
// JetStream, envelope nome/versione/id/timestamp/payload.
type NATSPublisher struct {
	pub *pkgevents.Publisher
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
	nc, err := nats.Connect(url)
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

	return &NATSPublisher{pub: pkgevents.NewPublisher(js)}, nc, nil
}

// Publish pubblica l'evento su JetStream con conferma sincrona (vedi
// pkg/events.Publisher.Publish) e scarta il risultato di conferma
// (stream/sequenza/duplicato): a core, che pubblica soltanto, basta sapere
// se l'operazione è riuscita o no.
func (p *NATSPublisher) Publish(ctx context.Context, name string, version int, payload any) error {
	_, err := p.pub.Publish(ctx, name, version, payload)
	return err
}
