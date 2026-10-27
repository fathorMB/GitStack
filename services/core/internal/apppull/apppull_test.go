package apppull

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMintVerify(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	id := uuid.New()
	tok, err := Mint([]string{"nuova", "vecchia"}, id, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "gsj_") || !Is(tok) || len(tok) > 128 {
		t.Fatalf("forma: %q (%d)", tok, len(tok))
	}
	got, err := Verify([]string{"nuova"}, tok, now.Add(time.Hour))
	if err != nil || got != id {
		t.Fatalf("verify: %v %v", got, err)
	}
	// Rotazione della chiave: il token firmato con la vecchia vale finché è nell'elenco.
	if got, err := Verify([]string{"altra", "nuova"}, tok, now); err != nil || got != id {
		t.Fatalf("doppia chiave: %v %v", got, err)
	}
	if _, err := Verify([]string{"altra"}, tok, now); err == nil {
		t.Fatal("chiave sconosciuta accettata")
	}
	if _, err := Verify([]string{"nuova"}, tok, now.Add(TTL)); err == nil {
		t.Fatal("token scaduto accettato")
	}
	tampered := tok[:len(tok)-2] + "AA"
	if _, err := Verify([]string{"nuova"}, tampered, now); err == nil {
		t.Fatal("token alterato accettato")
	}
	if _, err := Verify([]string{"nuova"}, "gsj_altro", now); err == nil {
		t.Fatal("token del job accettato")
	}
	if _, err := Mint(nil, id, now); err == nil {
		t.Fatal("senza chiave deve fallire")
	}
}
