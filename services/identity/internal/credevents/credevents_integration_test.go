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

	if got, err := svc.Ack(ctx, list[0].ID); err != nil || got != 1 {
		t.Fatalf("ack del primo: %d (%v)", got, err)
	}
	rest, _ := svc.Pending(ctx, 100)
	if len(rest) != 1 || rest[0].ID != list[1].ID {
		t.Fatalf("dopo l'ack: %+v", rest)
	}
	if got, _ := svc.Ack(ctx, list[1].ID+100); got != 1 {
		t.Fatalf("ack oltre l'ultimo: %d", got)
	}
	if got, _ := svc.Ack(ctx, list[1].ID); got != 0 {
		t.Fatalf("ack ripetuto: %d, innocuo", got)
	}
	if p, _ := svc.Pending(ctx, 100); len(p) != 0 {
		t.Fatalf("restano eventi: %+v", p)
	}
}
