package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReasonCredential: avviso di sicurezza sulle credenziali dell'utente (F13,
// ASVS V2.2.3, V2.3.3, V2.5.5). Senza repo né issue; event_name è
// credential.<tipo> e data ha il nome del token o della chiave (mai segreti).
const ReasonCredential = "credential"

// CredentialSource è identity vista da core: gli eventi sulle credenziali da
// consegnare e la loro conferma (implementato da identityclient.Client).
type CredentialSource interface {
	CredentialEvents(ctx context.Context, limit int) ([]identityclient.CredentialEvent, error)
	AckCredentialEvents(ctx context.Context, upTo int64) error
}

// credentialAdvisoryKey serializza i giri fra le repliche.
const credentialAdvisoryKey int64 = 0x6372656465 // "crede"

const credentialBatch = 100

// DefaultCredentialPoll è ogni quanto si chiede a identity se c'è qualcosa.
const DefaultCredentialPoll = 30 * time.Second

// CredentialNotifier trasforma gli eventi sulle credenziali di identity in
// notifiche in-app e, con l'SMTP attivo, in email (F13). Consegna almeno una
// volta: l'evento si conferma a identity solo dopo che la notifica è scritta.
// A differenza delle notifiche delle issue non dipende dalle preferenze email
// dell'utente: è un avviso di sicurezza.
type CredentialNotifier struct {
	Pool   *pgxpool.Pool
	Source CredentialSource
	Log    *slog.Logger
	Now    func() time.Time
	// EmailEnabled: l'SMTP è configurato, quindi l'avviso entra nella coda
	// delle email (EmailDispatcher).
	EmailEnabled bool
	Poll         time.Duration // default DefaultCredentialPoll
}

func (n *CredentialNotifier) defaults() {
	if n.Poll <= 0 {
		n.Poll = DefaultCredentialPoll
	}
	if n.Log == nil {
		n.Log = slog.Default()
	}
	if n.Now == nil {
		n.Now = time.Now
	}
}

// Run chiede a identity e consegna finché ctx non finisce.
func (n *CredentialNotifier) Run(ctx context.Context) {
	n.defaults()
	t := time.NewTicker(n.Poll)
	defer t.Stop()
	for {
		if _, err := n.Process(ctx); err != nil && ctx.Err() == nil {
			n.Log.Warn("avvisi sulle credenziali: giro non riuscito, si ritenta", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Process consegna gli eventi disponibili e ritorna quante notifiche ha scritto.
func (n *CredentialNotifier) Process(ctx context.Context) (int, error) {
	n.defaults()
	conn, err := n.Pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, credentialAdvisoryKey).Scan(&got); err != nil {
		return 0, err
	}
	if !got {
		return 0, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, credentialAdvisoryKey)
	}()

	total := 0
	for {
		events, err := n.Source.CredentialEvents(ctx, credentialBatch)
		if err != nil {
			return total, err
		}
		if len(events) == 0 {
			return total, nil
		}
		now := n.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return total, err
		}
		var last int64
		for _, ev := range events {
			data, merr := json.Marshal(ev.Detail)
			if merr != nil || ev.Detail == nil {
				data = []byte("{}")
			}
			var due *time.Time
			if n.EmailEnabled {
				due = &now
			}
			if _, err := tx.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, event_name, data, created_at, email_due_at)
				VALUES ($1, $2, 'credential', $3, $4, $5, $6)`,
				uuid.New(), ev.UserID, "credential."+ev.Kind, data, ev.CreatedAt, due); err != nil {
				_ = tx.Rollback(ctx)
				return total, fmt.Errorf("scrittura dell'avviso sulle credenziali: %w", err)
			}
			last = max(last, ev.ID)
		}
		if err := tx.Commit(ctx); err != nil {
			return total, err
		}
		total += len(events)
		if err := n.Source.AckCredentialEvents(ctx, last); err != nil {
			// Gli avvisi sono scritti: al giro dopo identity li ripropone e
			// si duplicano (consegna almeno una volta). Si segnala e si ferma.
			return total, fmt.Errorf("conferma degli eventi a identity: %w", err)
		}
		if len(events) < credentialBatch {
			return total, nil
		}
	}
}
