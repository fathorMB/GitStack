//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

func TestStore_CRUD(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	s := store.New(pool)

	created, err := s.Create(ctx, store.NewInput{
		Type:       "repo",
		Name:       "gitstack",
		Attributes: map[string]any{"visibility": "private"},
	})
	if err != nil {
		t.Fatalf("Create non riuscita: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create doveva generare un id non nullo")
	}
	if created.Type != "repo" {
		t.Fatalf("Type = %q, voluto repo", created.Type)
	}
	if created.Attributes["visibility"] != "private" {
		t.Fatalf("Attributes = %v, voluto visibility=private", created.Attributes)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get non riuscita: %v", err)
	}
	if got.Name != "gitstack" {
		t.Fatalf("Name = %q, voluto gitstack", got.Name)
	}

	newName := "gitstack-renamed"
	updated, err := s.Update(ctx, created.ID, store.UpdateInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update non riuscita: %v", err)
	}
	if updated.Name != newName {
		t.Fatalf("Name dopo update = %q, voluto %q", updated.Name, newName)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("UpdatedAt non è avanzato: prima %v, dopo %v", created.UpdatedAt, updated.UpdatedAt)
	}

	items, total, err := s.List(ctx, nil, nil, 1, 20)
	if err != nil {
		t.Fatalf("List non riuscita: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("List = %d items (total %d), voluto 1", len(items), total)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete non riuscita: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get dopo Delete = %v, voluto ErrNotFound", err)
	}
	if err := s.Delete(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Delete di una risorsa già eliminata = %v, voluto ErrNotFound", err)
	}
}

func TestStore_Create_ConflictOnDuplicateTypeAndName(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	s := store.New(pool)

	if _, err := s.Create(ctx, store.NewInput{Type: "repo", Name: "dup"}); err != nil {
		t.Fatalf("prima Create non riuscita: %v", err)
	}
	if _, err := s.Create(ctx, store.NewInput{Type: "repo", Name: "dup"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("seconda Create con stesso type+name = %v, voluto ErrConflict", err)
	}

	// Stesso name ma type diverso: non è un conflitto (la chiave unica è
	// (type, name), non name da solo).
	if _, err := s.Create(ctx, store.NewInput{Type: "app", Name: "dup"}); err != nil {
		t.Fatalf("Create con type diverso non deve andare in conflitto: %v", err)
	}
}

func TestStore_List_FilterByType(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	s := store.New(pool)

	if _, err := s.Create(ctx, store.NewInput{Type: "repo", Name: "r1"}); err != nil {
		t.Fatalf("Create repo non riuscita: %v", err)
	}
	if _, err := s.Create(ctx, store.NewInput{Type: "app", Name: "a1"}); err != nil {
		t.Fatalf("Create app non riuscita: %v", err)
	}

	repoType := "repo"
	items, total, err := s.List(ctx, &repoType, nil, 1, 20)
	if err != nil {
		t.Fatalf("List non riuscita: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Type != "repo" {
		t.Fatalf("List filtrata per type=repo = %+v (total %d), voluto 1 item di tipo repo", items, total)
	}
}

func TestStore_List_FilterByVisibleIDs(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	s := store.New(pool)

	var ids []uuid.UUID
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		r, err := s.Create(ctx, store.NewInput{Type: "repo", Name: n})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}

	// lista vuota: niente, total 0
	items, total, err := s.List(ctx, nil, []uuid.UUID{}, 1, 20)
	if err != nil || total != 0 || len(items) != 0 || items == nil {
		t.Fatalf("lista vuota: %v %d %v", items, total, err)
	}
	// tre visibili su cinque, perPage=2: total 3, pagina 2 con 1 item
	visible := []uuid.UUID{ids[4], ids[0], ids[2], uuid.New()}
	items, total, err = s.List(ctx, nil, visible, 1, 2)
	if err != nil || total != 3 || len(items) != 2 || items[0].ID != ids[0] || items[1].ID != ids[2] {
		t.Fatalf("pagina 1: %v %d %v", items, total, err)
	}
	items, total, err = s.List(ctx, nil, visible, 2, 2)
	if err != nil || total != 3 || len(items) != 1 || items[0].ID != ids[4] {
		t.Fatalf("pagina 2: %v %d %v", items, total, err)
	}
	// il filtro per tipo si somma a quello per id
	other := "app"
	items, total, err = s.List(ctx, &other, visible, 1, 20)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("tipo diverso: %v %d %v", items, total, err)
	}
}
