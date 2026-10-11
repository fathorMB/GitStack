// Package gitpush è il consumer di core dell'evento git.push (docs/events.md):
// un consumer JetStream durevole che decodifica la busta e chiama un Handler.
// È riusabile: i webhook (M-06/G) e il collegamento commit↔issue (M-06/C)
// ne usano uno ciascuno, con il proprio nome durevole, e non si
// disturbano.
package gitpush

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/nats-io/nats.go/jetstream"
)

// Durable dei consumer di core su git.push (convenzione <servizio>-<scopo>,
// docs/events.md: i nomi sono stabili, cambiarli riparte da zero).
const (
	DurableWebhooks = "core-webhooks-git"
	// DurableIssueLinks: collegamento commit↔issue e chiusura con fixes #n
	// (M-06/C, internal/issuelinks).
	DurableIssueLinks = "core-issue-linker"
	// DurableMirrors: mirror in push verso un altro server Git (V8, internal/mirrors).
	DurableMirrors = "core-mirrors-git"
)

// Handler gestisce un git.push. Un errore fa ritentare il messaggio (Nak con
// attesa); nil lo conferma.
type Handler func(ctx context.Context, env pkgevents.Envelope, p pkggitpush.Payload) error

// RetryDelay è l'attesa prima che NATS riconsegni un messaggio non gestito.
const RetryDelay = 5 * time.Second

// Run assicura lo stream GIT e il consumer durevole e consegna i messaggi a h
// finché ctx non finisce. Un messaggio illeggibile si scarta (Term): non
// diventerà mai valido.
func Run(ctx context.Context, js jetstream.JetStream, durable string, h Handler, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	domain := pkgevents.Domain(pkggitpush.Name)
	if _, err := pkgevents.EnsureStream(ctx, js, domain); err != nil {
		return err
	}
	stream, err := pkgevents.StreamName(domain)
	if err != nil {
		return err
	}
	cons, err := pkgevents.EnsureDurableConsumer(ctx, js, stream, durable, pkggitpush.Name)
	if err != nil {
		return err
	}
	reg := pkgevents.NewRegistry()
	pkggitpush.Register(reg)

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		env, err := pkgevents.UnmarshalEnvelope(msg.Data())
		if err != nil {
			log.Error("git.push: busta illeggibile, scartata", "durable", durable, "err", err)
			_ = msg.Term()
			return
		}
		dec, err := reg.Decode(env)
		var unknown *pkgevents.UnknownSchemaError
		switch {
		case errors.As(err, &unknown):
			log.Warn("git.push: versione dello schema sconosciuta, scartata", "durable", durable, "version", env.Version)
			_ = msg.Term()
			return
		case err != nil:
			log.Error("git.push: payload non valido, scartato", "durable", durable, "id", env.ID, "err", err)
			_ = msg.Term()
			return
		}
		p, ok := dec.(pkggitpush.Payload)
		if !ok {
			_ = msg.Term()
			return
		}
		hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := h(hctx, env, p); err != nil {
			log.Warn("git.push: gestione non riuscita, si ritenta", "durable", durable, "id", env.ID, "err", err)
			_ = msg.NakWithDelay(RetryDelay)
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("git.push: avvio del consumer %q: %w", durable, err)
	}
	<-ctx.Done()
	cc.Stop()
	return nil
}
