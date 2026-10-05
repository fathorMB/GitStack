//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
)

type fakeSink struct {
	mu    sync.Mutex
	fails int
	got   []pkgevents.Envelope
}

func (f *fakeSink) PublishEnvelope(_ context.Context, env pkgevents.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errors.New("nats non raggiungibile")
	}
	f.got = append(f.got, env)
	return nil
}

func TestEnqueue_RollbackNonLasciaNiente(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Enqueue(ctx, tx, "issue.created", 1, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("righe dopo il rollback: %d (%v)", n, err)
	}
	// Con il commit la riga c'è, con id e payload.
	tx, _ = pool.Begin(ctx)
	if err := outbox.Enqueue(ctx, tx, "issue.created", 1, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE sent_at IS NULL AND payload = '{"a": 1}'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("riga dopo il commit: %d (%v)", n, err)
	}
}

// Il relay ritenta con attesa crescente, tiene lo stesso id della busta a
// ogni tentativo e segna la riga inviata solo dopo l'ack.
func TestRelay_RitentaConLoStessoId(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	if err := outbox.Enqueue(ctx, pool, "repository.created", 1, map[string]any{"repo": map[string]any{"id": "r"}, "actor": nil}); err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{fails: 2}
	r := &outbox.Relay{Pool: pool, Sink: sink, BaseDelay: 200 * time.Millisecond, MaxDelay: time.Second}

	flush := func() int {
		n, err := r.Flush(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if flush() != 1 {
		t.Fatal("la riga doveva essere pronta")
	}
	if flush() != 0 { // in attesa: next_attempt_at nel futuro
		t.Fatal("ritentata prima dell'attesa")
	}
	time.Sleep(300 * time.Millisecond)
	flush() // secondo errore, attesa doppia
	var attempts int
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error FROM core.event_outbox`).Scan(&attempts, &lastErr); err != nil || attempts != 2 || lastErr == nil {
		t.Fatalf("attempts = %d, last_error = %v (%v)", attempts, lastErr, err)
	}
	time.Sleep(500 * time.Millisecond)
	flush() // riesce
	if len(sink.got) != 1 {
		t.Fatalf("pubblicati %d eventi", len(sink.got))
	}
	var id string
	var sent *time.Time
	if err := pool.QueryRow(ctx, `SELECT id::text, sent_at FROM core.event_outbox`).Scan(&id, &sent); err != nil || sent == nil || id != sink.got[0].ID {
		t.Fatalf("id outbox %s, busta %s, sent_at %v (%v)", id, sink.got[0].ID, sent, err)
	}
	if sink.got[0].Payload == nil || sink.got[0].Name != "repository.created" || sink.got[0].Version != 1 {
		t.Fatalf("busta = %+v", sink.got[0])
	}
	if flush() != 0 {
		t.Fatal("riga già inviata rilanciata")
	}
}

func TestMigration0009_SaleEScende(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exists := func() bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'event_outbox')`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !exists() {
		t.Fatal("event_outbox manca dopo le migrazioni")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'x.y', 1, '{}')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Down(ctx, pool, dsn, 1); err != nil {
		t.Fatalf("down 0009: %v", err)
	}
	if exists() {
		t.Fatal("event_outbox esiste dopo il down")
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
	if !exists() {
		t.Fatal("event_outbox manca dopo il nuovo up")
	}
}
