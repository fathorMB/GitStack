//go:build integration

package credevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/credevents"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// Eventi sulle credenziali (F13): scrittura, consegna con conferma, un solo
// avviso di scadenza per token e nessuno per token revocati o lontani.
func TestEventsLifecycle(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	us := users.New(pool, clock)
	alice, err := us.Create(ctx, users.CreateInput{Username: "alice", Email: "alice@example.com", DisplayName: "alice", Password: "una-password-molto-lunga-1"})
	if err != nil {
		t.Fatal(err)
	}
	svc := credevents.New(pool, clock)
	toks := apitokens.New(pool, clock, 365*24*time.Hour)

	if err := svc.Record(ctx, alice.ID, credevents.PasswordChanged, map[string]any{"by": "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Record(ctx, alice.ID, "inventato", nil); err == nil {
		t.Fatal("un tipo sconosciuto doveva essere rifiutato dal database")
	}

	mk := func(name string, in time.Duration) {
		t.Helper()
		exp := now.Add(in)
		if _, _, err := toks.Create(ctx, apitokens.CreateInput{UserID: alice.ID, Name: name, Scopes: []string{"read:user"}, ExpiresAt: &exp}); err != nil {
			t.Fatal(err)
		}
	}
	mk("vicino", 48*time.Hour)
	mk("lontano", 30*24*time.Hour)
	mk("revocato", 24*time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE identity.api_tokens SET revoked_at = now() WHERE name = 'revocato'`); err != nil {
		t.Fatal(err)
	}

	n, err := svc.ScanExpiring(ctx)
	if err != nil || n != 1 {
		t.Fatalf("scadenze trovate = %d (%v), attesa 1 (solo «vicino»)", n, err)
	}
	if n, _ := svc.ScanExpiring(ctx); n != 0 {
		t.Fatalf("secondo giro: %d avvisi, uno solo per token", n)
	}

	list, err := svc.Pending(ctx, 100)
	if err != nil || len(list) != 2 {
		t.Fatalf("eventi in attesa = %d (%v), attesi 2", len(list), err)
	}
	if list[0].Kind != credevents.PasswordChanged || list[1].Kind != credevents.TokenExpiring || list[1].Detail["name"] != "vicino" {
		t.Fatalf("eventi: %+v", list)
	}
	if _, leak := list[1].Detail["token"]; leak {
		t.Fatal("il dettaglio non deve contenere il token")
	}

	if got, err := svc.Ack(ctx, []int64{list[0].ID}); err != nil || got != 1 {
		t.Fatalf("ack del primo: %d (%v)", got, err)
	}
	rest, _ := svc.Pending(ctx, 100)
	if len(rest) != 1 || rest[0].ID != list[1].ID {
		t.Fatalf("dopo l'ack: %+v", rest)
	}

	// Un evento con id minore committato dopo la lettura non si perde: nella
	// sequence gli id non seguono l'ordine di commit. Si simula con id espliciti:
	// l'evento alto è già committato quando core legge, quello basso arriva dopo.
	base := list[1].ID
	insert := func(id int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO identity.credential_events (id, user_id, kind, detail)
			OVERRIDING SYSTEM VALUE VALUES ($1, $2, 'ssh_key_added', '{}')`, id, alice.ID); err != nil {
			t.Fatal(err)
		}
	}
	insert(base + 10)
	read, err := svc.Pending(ctx, 100)
	if err != nil || len(read) != 2 || read[1].ID != base+10 {
		t.Fatalf("lettura: %+v (%v)", read, err)
	}
	insert(base + 5) // committato dopo la lettura, con id minore dell'ultimo letto
	if got, err := svc.Ack(ctx, []int64{read[0].ID, read[1].ID}); err != nil || got != 2 {
		t.Fatalf("ack di quanto letto: %d (%v)", got, err)
	}
	p, _ := svc.Pending(ctx, 100)
	if len(p) != 1 || p[0].ID != base+5 {
		t.Fatalf("l'evento con id minore inserito dopo la lettura doveva restare pending: %+v", p)
	}
	if got, _ := svc.Ack(ctx, []int64{read[0].ID, base + 1000}); got != 0 {
		t.Fatalf("ack ripetuto o di id inesistenti: %d, innocuo", got)
	}
	if got, _ := svc.Ack(ctx, nil); got != 0 {
		t.Fatalf("ack vuoto: %d", got)
	}
	if got, _ := svc.Ack(ctx, []int64{base + 5}); got != 1 {
		t.Fatalf("ack dell'evento tardivo: %d", got)
	}
	if p, _ := svc.Pending(ctx, 100); len(p) != 0 {
		t.Fatalf("restano eventi: %+v", p)
	}
}
