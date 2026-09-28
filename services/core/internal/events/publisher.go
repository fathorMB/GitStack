// Package events definisce il punto di aggancio con cui core pubblica i
// propri eventi di dominio sul bus NATS JetStream, senza legare il resto
// del servizio a una libreria concreta.
//
// Stato (GIT-5, nota vincolante del CTO): l'evento di prova si pubblica con
// la libreria condivisa di GIT-6 (pkg/events del workspace, ora su main),
// non con codice NATS scritto a mano qui. NATSPublisher (vedi nats.go) è
// l'adapter che implementa Publisher chiamando pkg/events: nome, versione e
// payload dell'evento di prova sono quelli di pkg/events/testevent (fonte
// di verità unica, GIT-6), non ridefiniti qui. NoopPublisher resta
// disponibile (test, o un ambiente senza NATS) ma non è più quello
// collegato in produzione (vedi main.go).
package events

import (
	"context"
	"log/slog"

	"github.com/fathorMB/GitStack/pkg/events/testevent"
)

// TestResourceCreatedName, TestResourceCreatedVersion e
// TestResourceCreatedPayload sono alias dello schema definito in
// pkg/events/testevent (GIT-6): nome, versione e payload dell'evento di
// prova pubblicato da core alla creazione della risorsa di prova (criterio
// aggiuntivo confermato dal CTO su GIT-5 e GIT-6). Alias, non una
// ridefinizione: un solo schema, non due che potrebbero divergere.
const (
	TestResourceCreatedName    = testevent.Name
	TestResourceCreatedVersion = testevent.Version
)

// TestResourceCreatedPayload è un alias di tipo (non un tipo nuovo) di
// testevent.Payload: i valori dei due tipi sono intercambiabili.
type TestResourceCreatedPayload = testevent.Payload

// Publisher pubblica un evento di dominio col nome e la versione di schema
// dati. L'implementazione concreta (NATSPublisher, vedi nats.go: libreria
// di GIT-6, envelope NATS JetStream con nome/versione/id/timestamp/payload)
// si aggancia qui senza cambiare i chiamanti.
type Publisher interface {
	Publish(ctx context.Context, name string, version int, payload any) error
}

// NoopPublisher logga l'evento invece di pubblicarlo: utile nei test o come
// scelta esplicita in un ambiente senza NATS. In produzione core usa
// NATSPublisher (vedi main.go). Non fa fallire le richieste HTTP: la
// creazione della risorsa resta l'operazione principale, la pubblicazione
// dell'evento è collaterale.
type NoopPublisher struct {
	Logger *slog.Logger
}

var _ Publisher = NoopPublisher{}

func (p NoopPublisher) Publish(_ context.Context, name string, version int, payload any) error {
	if p.Logger != nil {
		p.Logger.Warn("evento non pubblicato: libreria eventi di GIT-6 non ancora agganciata",
			"event_name", name,
			"event_version", version,
			"payload", payload,
		)
	}
	return nil
}
