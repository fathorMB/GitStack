package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sink pubblica una busta già costruita, con ack (NATSPublisher).
type Sink interface {
	PublishEnvelope(ctx context.Context, env pkgevents.Envelope) error
}

// Users risolve gli id utente in nome e tipo (identity). Il relay completa
// con actor.type e assignee.type i payload scritti senza tipo.
type Users interface {
	LookupUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]identityclient.CodeUser, error)
}

// Relay pubblica le righe non inviate dell'outbox. Sicuro con più repliche
// (FOR UPDATE SKIP LOCKED). L'ordine è quello di inserimento nel lotto, ma
// non è garantito fra un tentativo e l'altro: i payload sono istantanee
// dello stato dopo la modifica.
type Relay struct {
	Pool *pgxpool.Pool
	Sink Sink
	// Users può essere nil (nessuna identity): il tipo dell'utente è human.
	Users Users
	Log   *slog.Logger

	Poll           time.Duration // default 1s
	Batch          int           // default 50
	BaseDelay      time.Duration // prima attesa dopo un errore; default 2s, raddoppia
	MaxDelay       time.Duration // default 5m
	PublishTimeout time.Duration // per evento; default 5s
	Retention      time.Duration // le righe inviate si tengono così a lungo; default 7 giorni

	lastCleanup time.Time
}

func (r *Relay) defaults() {
	if r.Poll <= 0 {
		r.Poll = time.Second
	}
	if r.Batch <= 0 {
		r.Batch = 50
	}
	if r.BaseDelay <= 0 {
		r.BaseDelay = 2 * time.Second
	}
	if r.MaxDelay <= 0 {
		r.MaxDelay = 5 * time.Minute
	}
	if r.PublishTimeout <= 0 {
		r.PublishTimeout = 5 * time.Second
	}
	if r.Retention <= 0 {
		r.Retention = 7 * 24 * time.Hour
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
}

// Run pubblica finché ctx non finisce. Parte subito (dopo un riavvio le
// righe pendenti vanno via senza aspettare il primo tick).
func (r *Relay) Run(ctx context.Context) {
	r.defaults()
	t := time.NewTicker(r.Poll)
	defer t.Stop()
	for {
		for {
			n, err := r.Flush(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.Log.Warn("outbox: invio degli eventi non riuscito", "err", err)
				}
				break
			}
			if n < r.Batch {
				break
			}
		}
		r.cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type pending struct {
	seq      int64
	id       uuid.UUID
	name     string
	version  int
	payload  []byte
	created  time.Time
	attempts int
}

// Flush pubblica un lotto di righe pronte e ritorna quante ne ha lette. Un
// errore di pubblicazione non è un errore di Flush: la riga resta pendente
// con attempts+1 e next_attempt_at spostato in avanti.
func (r *Relay) Flush(ctx context.Context) (int, error) {
	r.defaults()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `SELECT seq, id, name, version, payload, created_at, attempts FROM core.event_outbox
		WHERE sent_at IS NULL AND next_attempt_at <= clock_timestamp()
		ORDER BY seq LIMIT $1 FOR UPDATE SKIP LOCKED`, r.Batch)
	if err != nil {
		return 0, err
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.seq, &p.id, &p.name, &p.version, &p.payload, &p.created, &p.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	kinds, kerr := r.userKinds(ctx, batch)
	for _, p := range batch {
		err := kerr
		if err == nil {
			err = r.publish(ctx, p, kinds)
		}
		if err != nil {
			r.Log.Warn("outbox: evento non pubblicato, si ritenta", "event", p.name, "id", p.id, "attempts", p.attempts+1, "err", err)
			if _, uerr := tx.Exec(ctx, `UPDATE core.event_outbox SET attempts = attempts + 1, last_error = $2,
				next_attempt_at = clock_timestamp() + make_interval(secs => $3) WHERE seq = $1`,
				p.seq, err.Error(), r.delay(p.attempts).Seconds()); uerr != nil {
				return len(batch), uerr
			}
			if kerr == nil {
				// NATS non risponde: inutile aspettare il timeout per ogni riga
				// del lotto, le altre restano pronte per il prossimo giro.
				break
			}
			continue
		}
		if _, uerr := tx.Exec(ctx, `UPDATE core.event_outbox SET sent_at = clock_timestamp(), attempts = attempts + 1, last_error = NULL WHERE seq = $1`, p.seq); uerr != nil {
			return len(batch), uerr
		}
	}
	return len(batch), tx.Commit(ctx)
}

// delay è l'attesa dopo il (attempts+1)-esimo errore: Base·2^attempts, al massimo MaxDelay.
func (r *Relay) delay(attempts int) time.Duration {
	d := r.BaseDelay
	for i := 0; i < attempts && d < r.MaxDelay; i++ {
		d *= 2
	}
	return min(d, r.MaxDelay)
}

func (r *Relay) publish(ctx context.Context, p pending, kinds map[uuid.UUID]string) error {
	payload, err := fillUserTypes(p.payload, kinds)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, r.PublishTimeout)
	defer cancel()
	return r.Sink.PublishEnvelope(pctx, pkgevents.Envelope{
		Name: p.name, Version: p.version, ID: p.id.String(), Time: p.created.UTC(), Payload: payload,
	})
}

// userRefs sono i punti del payload con un utente: chi ha agito e l'assegnatario.
var userRefs = []string{"actor", "assignee"}

// userKinds risolve in un colpo solo il tipo degli utenti senza tipo del lotto.
func (r *Relay) userKinds(ctx context.Context, batch []pending) (map[uuid.UUID]string, error) {
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, p := range batch {
		var top map[string]json.RawMessage
		if json.Unmarshal(p.payload, &top) != nil {
			continue
		}
		for _, k := range userRefs {
			if id, ok := untypedUser(top[k]); ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	kinds := map[uuid.UUID]string{}
	if len(ids) == 0 || r.Users == nil {
		return kinds, nil
	}
	found, err := r.Users.LookupUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("risoluzione del tipo degli utenti: %w", err)
	}
	for id, u := range found {
		kinds[id] = u.Kind
	}
	return kinds, nil
}

// untypedUser ritorna l'id di un utente del payload che non ha ancora il tipo.
func untypedUser(raw json.RawMessage) (uuid.UUID, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return uuid.Nil, false
	}
	var u struct{ ID, Type string }
	if json.Unmarshal(raw, &u) != nil || u.Type != "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(u.ID)
	return id, err == nil
}

// fillUserTypes completa actor.type e assignee.type: il tipo (human|agent)
// lo conosce identity, non la richiesta che ha generato l'evento. Un utente
// che identity non conosce (eliminato) o senza identity configurata è human.
func fillUserTypes(payload []byte, kinds map[uuid.UUID]string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		return nil, err
	}
	changed := false
	for _, k := range userRefs {
		if _, ok := untypedUser(top[k]); !ok {
			continue
		}
		var u map[string]any
		if err := json.Unmarshal(top[k], &u); err != nil {
			return nil, err
		}
		kind := "human"
		if id, err := uuid.Parse(fmt.Sprint(u["id"])); err == nil && kinds[id] != "" {
			kind = kinds[id]
		}
		u["type"] = kind
		b, err := json.Marshal(u)
		if err != nil {
			return nil, err
		}
		top[k] = b
		changed = true
	}
	if !changed {
		return payload, nil
	}
	return json.Marshal(top)
}

// cleanup toglie le righe inviate da più di Retention (al massimo una volta all'ora).
func (r *Relay) cleanup(ctx context.Context) {
	if time.Since(r.lastCleanup) < time.Hour {
		return
	}
	r.lastCleanup = time.Now()
	if _, err := r.Pool.Exec(ctx, `DELETE FROM core.event_outbox WHERE sent_at IS NOT NULL
		AND sent_at < clock_timestamp() - make_interval(secs => $1)`, r.Retention.Seconds()); err != nil && ctx.Err() == nil {
		r.Log.Warn("outbox: pulizia delle righe inviate non riuscita", "err", err)
	}
}
