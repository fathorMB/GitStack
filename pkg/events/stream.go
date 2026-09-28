package events

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// EnsureStream crea (se manca) o aggiorna lo stream JetStream del dominio
// indicato, con subject "<dominio>.>" (convenzioni: docs/events.md). Va
// chiamata dal servizio che pubblica sul dominio, prima di Publisher.Publish:
// JetStream rifiuta la pubblicazione su subject non coperti da uno stream.
func EnsureStream(ctx context.Context, js jetstream.JetStream, domain string) (jetstream.Stream, error) {
	name, err := StreamName(domain)
	if err != nil {
		return nil, err
	}
	cfg := jetstream.StreamConfig{
		Name:        name,
		Description: fmt.Sprintf("Eventi GitStack del dominio %q (docs/events.md)", domain),
		Subjects:    []string{domain + ".>"},
	}
	stream, err := js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("events: crea/aggiorna stream %q per dominio %q: %w", name, domain, err)
	}
	return stream, nil
}

// EnsureDurableConsumer crea (se manca) o riusa un consumer pull durevole
// sullo stream indicato, filtrato su un subject (evento o wildcard come
// "issue.*"). Un consumer durevole riprende dall'ultimo messaggio confermato
// anche dopo un riavvio del servizio consumatore: il nome durevole (vedi
// docs/events.md per la convenzione) è la sua identità persistente.
func EnsureDurableConsumer(ctx context.Context, js jetstream.JetStream, streamName, durableName, filterSubject string) (jetstream.Consumer, error) {
	cfg := jetstream.ConsumerConfig{
		Durable:       durableName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: filterSubject,
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, streamName, cfg)
	if err != nil {
		return nil, fmt.Errorf("events: crea/aggiorna consumer durevole %q su stream %q: %w", durableName, streamName, err)
	}
	return cons, nil
}
