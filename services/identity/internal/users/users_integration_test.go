//go:build integration

package users_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

const pw = "una password lunga e buona"

func newSvc(t *testing.T) *users.Service {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	return users.New(pool, nil)
}

func TestCreateAndGet(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	u, err := s.Create(ctx, users.CreateInput{Username: "alice", Email: "Alice@Example.com", DisplayName: "Alice", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	if u.Kind != "human" || !u.IsActive || u.IsAdmin || u.Email == nil {
		t.Fatalf("default inattesi: %+v", u)
	}
	got, err := s.Get(ctx, "alice")
	if err != nil || got.ID != u.ID {
		t.Fatalf("Get: %+v %v", got, err)
	}
	if _, err := s.Get(ctx, "nessuno"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("Get inesistente: %v", err)
	}
	// Agente senza password.
	if _, err := s.Create(ctx, users.CreateInput{Username: "bot", Kind: "agent"}); err != nil {
		t.Fatalf("agente: %v", err)
	}
}

func TestCreateValidationAndConflicts(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	var ve *users.ValidationError
	for name, in := range map[string]users.CreateInput{
		"username":    {Username: "Alice_"},
		"email":       {Username: "x1", Email: "non-una-email"},
		"password":    {Username: "x2", Password: "corta"},
		"kind":        {Username: "x3", Kind: "robot"},
		"displayName": {Username: "x4", DisplayName: string(make([]rune, 129))},
	} {
		_, err := s.Create(ctx, in)
		if !errors.As(err, &ve) || ve.Fields[name] == "" {
			t.Errorf("%s: atteso ValidationError sul campo, ottenuto %v", name, err)
		}
	}
	if _, err := s.Create(ctx, users.CreateInput{Username: "bot", Kind: "agent", Password: pw}); !errors.As(err, &ve) {
		t.Errorf("agente con password accettato: %v", err)
	}
	if _, err := s.Create(ctx, users.CreateInput{Username: "alice", Email: "a@example.com", Password: pw}); err != nil {
		t.Fatal(err)
	}
	var ae *users.AlreadyExistsError
	if _, err := s.Create(ctx, users.CreateInput{Username: "alice"}); !errors.As(err, &ae) || ae.Field != "username" {
		t.Fatalf("username duplicato: %v", err)
	}
	if _, err := s.Create(ctx, users.CreateInput{Username: "alice2", Email: "A@EXAMPLE.com"}); !errors.As(err, &ae) || ae.Field != "email" {
		t.Fatalf("email duplicata (case-insensitive): %v", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, users.CreateInput{Username: "alice", Password: pw}); err != nil {
		t.Fatal(err)
	}
	dn, bio, av, em := "Alice A.", "ciao", "https://example.com/a.png", "alice@example.com"
	u, err := s.Update(ctx, "alice", users.UpdateInput{DisplayName: &dn, Bio: &bio, AvatarURL: &av, Email: &em})
	if err != nil || u.DisplayName != dn || u.Bio != bio || u.AvatarURL == nil || *u.AvatarURL != av || *u.Email != em {
		t.Fatalf("Update: %+v %v", u, err)
	}
	empty := ""
	u, _ = s.Update(ctx, "alice", users.UpdateInput{AvatarURL: &empty})
	if u.AvatarURL != nil {
		t.Fatal("avatar non cancellato")
	}
	bad := "ftp://x"
	var ve *users.ValidationError
	if _, err := s.Update(ctx, "alice", users.UpdateInput{AvatarURL: &bad}); !errors.As(err, &ve) {
		t.Fatalf("avatar non valido accettato: %v", err)
	}
	if _, err := s.Update(ctx, "nessuno", users.UpdateInput{Bio: &bio}); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("Update inesistente: %v", err)
	}
}

func TestLastAdminProtected(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, users.CreateInput{Username: "root", Password: pw, IsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	f := false
	if _, err := s.Update(ctx, "root", users.UpdateInput{IsAdmin: &f}); !errors.Is(err, users.ErrLastAdmin) {
		t.Fatalf("degradare l'ultimo admin: %v", err)
	}
	if _, err := s.Update(ctx, "root", users.UpdateInput{IsActive: &f}); !errors.Is(err, users.ErrLastAdmin) {
		t.Fatalf("disattivare l'ultimo admin: %v", err)
	}
	if err := s.Delete(ctx, "root"); !errors.Is(err, users.ErrLastAdmin) {
		t.Fatalf("eliminare l'ultimo admin: %v", err)
	}
	if _, err := s.Create(ctx, users.CreateInput{Username: "root2", Password: pw, IsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "root"); err != nil {
		t.Fatalf("con un altro admin si può eliminare: %v", err)
	}
	if err := s.Delete(ctx, "root"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("doppia eliminazione: %v", err)
	}
}

func TestListAndSearch(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	for _, n := range []string{"anna", "andrea", "bruno", "a-b"} {
		if _, err := s.Create(ctx, users.CreateInput{Username: n, DisplayName: "Nome " + n}); err != nil {
			t.Fatal(err)
		}
	}
	all, total, err := s.List(ctx, "", 1, 20)
	if err != nil || total != 4 || len(all) != 4 || all[0].Username != "a-b" {
		t.Fatalf("List: %d %d %v", total, len(all), err)
	}
	got, total, _ := s.List(ctx, "an", 1, 20)
	if total != 2 || len(got) != 2 {
		t.Fatalf("prefisso 'an': total=%d", total)
	}
	if _, total, _ := s.List(ctx, "%", 1, 20); total != 0 {
		t.Fatalf("il carattere %% deve essere letterale: total=%d", total)
	}
	page, total, _ := s.List(ctx, "", 2, 3)
	if total != 4 || len(page) != 1 {
		t.Fatalf("paginazione: total=%d len=%d", total, len(page))
	}
}
