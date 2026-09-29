//go:build integration

package userkeys_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/userkeys"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func pub(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../sshkeys/testdata/" + name + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func setup(t *testing.T) (*userkeys.Service, *clock, uuid.UUID, uuid.UUID, func(string) string) {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	mk := func(n string) uuid.UUID {
		u, err := us.Create(context.Background(), users.CreateInput{Username: n, Email: n + "@example.com", DisplayName: n, Password: "una password lunga e buona"})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	return userkeys.New(pool, c.now), c, mk("alice"), mk("bob"), func(n string) string { return pub(t, n) }
}

func TestAddListGetDelete(t *testing.T) {
	s, _, alice, bob, key := setup(t)
	ctx := context.Background()
	k, err := s.Add(ctx, alice, "laptop", key("ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("../sshkeys/testdata/ed25519.fingerprint")
	if k.KeyType != "ssh-ed25519" || k.Fingerprint != strings.TrimSpace(string(want)) {
		t.Fatalf("chiave: %+v", k)
	}
	if _, err := s.Add(ctx, alice, "rsa", key("rsa3072")); err != nil {
		t.Fatal(err)
	}
	list, total, err := s.List(ctx, alice, 1, 20)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("elenco: %v %d", err, total)
	}
	if _, n, _ := s.List(ctx, bob, 1, 20); n != 0 {
		t.Fatal("bob vede le chiavi di alice")
	}
	if _, err := s.Get(ctx, bob, k.ID); !errors.Is(err, userkeys.ErrNotFound) {
		t.Fatalf("get altrui: %v", err)
	}
	if err := s.Delete(ctx, bob, k.ID); !errors.Is(err, userkeys.ErrNotFound) {
		t.Fatalf("delete altrui: %v", err)
	}
	if err := s.Delete(ctx, alice, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, alice, k.ID); !errors.Is(err, userkeys.ErrNotFound) {
		t.Fatalf("get dopo delete: %v", err)
	}
	// Cancellata, la chiave si può registrare di nuovo (anche da un altro).
	if _, err := s.Add(ctx, bob, "mia", key("ed25519")); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicates(t *testing.T) {
	s, _, alice, bob, key := setup(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, alice, "laptop", key("ed25519")); err != nil {
		t.Fatal(err)
	}
	var fe *userkeys.FingerprintInUseError
	if _, err := s.Add(ctx, bob, "altro", key("ed25519")); !errors.As(err, &fe) {
		t.Fatalf("chiave di un altro utente: %v", err)
	}
	if _, err := s.Add(ctx, alice, "altro", key("ed25519")); !errors.As(err, &fe) {
		t.Fatalf("stessa chiave dello stesso utente: %v", err)
	}
	var te *userkeys.TitleInUseError
	if _, err := s.Add(ctx, alice, "laptop", key("ecdsa256")); !errors.As(err, &te) {
		t.Fatalf("titolo duplicato: %v", err)
	}
	if _, err := s.Add(ctx, bob, "laptop", key("ecdsa256")); err != nil {
		t.Fatalf("stesso titolo su un altro utente: %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, _, alice, _, key := setup(t)
	ctx := context.Background()
	var ve *userkeys.ValidationError
	if _, err := s.Add(ctx, alice, "x", key("dsa")); !errors.As(err, &ve) || ve.Fields["publicKey"] == "" {
		t.Fatalf("DSA: %v", err)
	}
	if _, err := s.Add(ctx, alice, "x", "non è una chiave"); !errors.As(err, &ve) {
		t.Fatalf("testo libero: %v", err)
	}
	if _, err := s.Add(ctx, alice, "", key("ed25519")); !errors.As(err, &ve) || ve.Fields["title"] == "" {
		t.Fatalf("titolo vuoto: %v", err)
	}
}

func TestLookupUpdatesLastUsed(t *testing.T) {
	s, c, alice, _, key := setup(t)
	ctx := context.Background()
	k, _ := s.Add(ctx, alice, "laptop", key("ed25519"))
	got, err := s.Lookup(ctx, k.Fingerprint)
	if err != nil || got.UserID != alice || got.LastUsedAt == nil || !got.LastUsedAt.Equal(c.t) {
		t.Fatalf("lookup: %v %+v", err, got)
	}
	if _, err := s.Lookup(ctx, "SHA256:"+strings.Repeat("A", 43)); !errors.Is(err, userkeys.ErrNotFound) {
		t.Fatalf("sconosciuta: %v", err)
	}
}
