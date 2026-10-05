// Package outbox è l'outbox transazionale degli eventi di dominio di core
// (M-06/B, GIT-130).
//
// Pubblicazione affidabile: chi cambia lo stato chiama Enqueue con la
// transazione della modifica (pgx.Tx), quindi la riga dell'evento esiste se
// e solo se la modifica è andata a buon fine (niente evento per
// un'operazione fallita), e un evento confermato non si perde nemmeno se
// NATS è irraggiungibile o core si riavvia: il Relay pubblica le righe non
// inviate con Publish con ack di JetStream, le segna inviate e ritenta con
// attesa crescente. L'id della busta è fissato alla scrittura, ed è anche il
// Nats-Msg-Id: un doppio invio (relay morto fra ack e UPDATE) è idempotente
// per i consumatori.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer è ciò che serve a Enqueue: una transazione (o il pool, nei test).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Enqueue scrive l'evento nell'outbox, nella transazione q. Il payload è
// serializzato subito: un payload non serializzabile fa fallire la
// transazione, non il relay.
func Enqueue(ctx context.Context, q Execer, name string, version int, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox: payload di %q: %w", name, err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES ($1, $2, $3, $4)`,
		uuid.New(), name, version, raw); err != nil {
		return fmt.Errorf("outbox: scrittura di %q: %w", name, err)
	}
	return nil
}
