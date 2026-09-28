// Package events definisce il punto di aggancio con cui core pubblica i
// propri eventi di dominio sul bus NATS JetStream, senza legare il resto
// del servizio a una libreria concreta.
//
// Stato (GIT-5, nota vincolante del CTO): l'evento di prova si pubblica con
// la libreria condivisa di GIT-6 (pkg/events del workspace), non con codice
// NATS scritto a mano qui. Al momento di questo commit GIT-6 è ancora in
// corso (libreria non ancora committata/approvata): Publisher espone già
// l'interfaccia che core usa per pubblicare (vedi httpserver.resourceServer
// .publisher), con un'implementazione NoopPublisher che logga soltanto.
// Quando GIT-6 è approvato, il collegamento è un cambio locale a questo
// pacchetto (un adapter che implementa Publisher chiamando pkg/events) più
// il test d'integrazione con NATS reale richiesto dal criterio aggiuntivo
// di GIT-5; nessuna altra parte di core cambia.
package events

import (
	"context"
	"log/slog"
)

// TestResourceCreated è l'evento di prova pubblicato da core alla creazione
// della risorsa di prova (criterio aggiuntivo confermato dal CTO su GIT-5 e
// GIT-6): nome e versione dello schema, così un consumer può validare il
// payload e scartare senza crash le versioni che non conosce (criterio di
// GIT-6).
const (
	TestResourceCreatedName    = "core.resource.test.created"
	TestResourceCreatedVersion = 1
)

// TestResourceCreatedPayload è il payload dell'evento TestResourceCreated.
type TestResourceCreatedPayload struct {
	ResourceID string `json:"resourceId"`
	Type       string `json:"type"`
	Name       string `json:"name"`
}

// Publisher pubblica un evento di dominio col nome e la versione di schema
// dati. L'implementazione concreta (libreria di GIT-6, envelope NATS
// JetStream con nome/versione/id/timestamp/payload) si aggancia qui senza
// cambiare i chiamanti.
type Publisher interface {
	Publish(ctx context.Context, name string, version int, payload any) error
}

// NoopPublisher logga l'evento invece di pubblicarlo: usato finché la
// libreria di GIT-6 non è disponibile in questo modulo. Non fa fallire le
// richieste HTTP: la creazione della risorsa resta l'operazione principale,
// la pubblicazione dell'evento è collaterale.
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
