//go:build integration

package users_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

func TestLookupByEmails(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, users.CreateInput{Username: "alice", Email: "Alice@Example.com", Password: pw}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, users.CreateInput{Username: "botty", Kind: "agent", Email: "botty@agents.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupByEmails(ctx, []string{"alice@example.COM", "BOTTY@agents.example.com", "ignoto@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	byEmail := map[string]users.EmailMatch{}
	for _, m := range got {
		byEmail[m.Email] = m
	}
	if len(got) != 2 {
		t.Fatalf("attesi 2 utenti (l'email ignota manca), ottenuti %+v", got)
	}
	if m := byEmail["alice@example.COM"]; m.Username != "alice" || m.Kind != "human" || m.Email != "alice@example.com" {
		t.Errorf("alice: %+v", m)
	}
	if m := byEmail["BOTTY@agents.example.com"]; m.Username != "botty" || m.Kind != "agent" {
		t.Errorf("botty: %+v", m)
	}
	if none, err := s.LookupByEmails(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("nessuna email: %v %v", none, err)
	}
}

func TestLookupByIDs(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	alice, err := s.Create(ctx, users.CreateInput{Username: "alice", Email: "alice@example.com", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	botty, err := s.Create(ctx, users.CreateInput{Username: "botty", Kind: "agent", Email: "botty@agents.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupByIDs(ctx, []uuid.UUID{alice.ID, botty.ID, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]users.IDMatch{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if len(got) != 2 {
		t.Fatalf("attesi 2 utenti (l'id ignoto manca), ottenuti %+v", got)
	}
	if m := byID[alice.ID]; m.Username != "alice" || m.Kind != "human" || m.Email != "alice@example.com" {
		t.Errorf("alice: %+v", m)
	}
	if m := byID[botty.ID]; m.Username != "botty" || m.Kind != "agent" {
		t.Errorf("botty: %+v", m)
	}
	if none, err := s.LookupByIDs(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("nessun id: %v %v", none, err)
	}
}
