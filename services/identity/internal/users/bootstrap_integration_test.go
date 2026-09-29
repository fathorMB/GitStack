//go:build integration

package users_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

const initialPw = "password-iniziale-generata-1234"

func adminRows(t *testing.T, s *users.Service) (n int) {
	t.Helper()
	list, _, err := s.List(context.Background(), "", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range list {
		if u.IsAdmin {
			n++
		}
	}
	return n
}

func TestBootstrapAdminOnEmptyDBAndIdempotent(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	s := users.New(pool, nil)
	ctx := context.Background()

	created, err := s.BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: initialPw})
	if err != nil || !created {
		t.Fatalf("primo bootstrap: created=%v err=%v", created, err)
	}
	u, err := s.Get(ctx, "admin")
	if err != nil || !u.IsAdmin || !u.IsActive {
		t.Fatalf("admin: %+v %v", u, err)
	}
	must, err := s.MustChangePassword(ctx, u.ID)
	if err != nil || !must {
		t.Fatalf("must_change atteso true: %v %v", must, err)
	}
	q := `SELECT secret_hash, updated_at::text FROM identity.credentials WHERE user_id=$1 AND kind='password'`
	var hash1, upd1 string
	if err := pool.QueryRow(ctx, q, u.ID).Scan(&hash1, &upd1); err != nil {
		t.Fatal(err)
	}

	// Secondo bootstrap, anche con un'altra password: nessuna modifica.
	created, err = s.BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: "un-altra-password-diversa-99"})
	if err != nil || created {
		t.Fatalf("secondo bootstrap: created=%v err=%v", created, err)
	}
	var hash2, upd2 string
	if err := pool.QueryRow(ctx, q, u.ID).Scan(&hash2, &upd2); err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 || upd1 != upd2 {
		t.Fatal("il secondo bootstrap ha modificato la credenziale")
	}
	u2, _ := s.Get(ctx, "admin")
	if !u2.UpdatedAt.Equal(u.UpdatedAt) || adminRows(t, s) != 1 {
		t.Fatalf("utente cambiato o admin != 1: %+v", u2)
	}
}

// Su un DB già inizializzato (un admin con altro nome) non succede nulla.
func TestBootstrapAdminSkipsWhenAdminExists(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	s := users.New(pool, nil)
	ctx := context.Background()
	if _, err := s.Create(ctx, users.CreateInput{Username: "root", Password: pw, IsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	created, err := s.BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: initialPw})
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if _, err := s.Get(ctx, "admin"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("admin non doveva essere creato: %v", err)
	}
}

func TestBootstrapAdminConcurrent(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	const n = 8
	var wg sync.WaitGroup
	res := make(chan bool, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Un Service per "replica".
			c, err := users.New(pool, nil).BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: initialPw})
			if err != nil {
				errs <- err
				return
			}
			res <- c
		}()
	}
	wg.Wait()
	close(res)
	close(errs)
	for err := range errs {
		t.Fatalf("errore in un bootstrap concorrente: %v", err)
	}
	created := 0
	for c := range res {
		if c {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("creazioni = %d, atteso 1", created)
	}
	if got := adminRows(t, users.New(pool, nil)); got != 1 {
		t.Fatalf("admin = %d, atteso 1", got)
	}
}

func TestBootstrapAdminRejectsWeakPasswordWithoutLeaking(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	s := users.New(pool, nil)
	_, err := s.BootstrapAdmin(context.Background(), users.BootstrapAdminInput{Username: "admin", Password: "corta"})
	if err == nil {
		t.Fatal("attesa un'errore di validazione")
	}
	if strings.Contains(err.Error(), "corta") {
		t.Fatalf("la password è nell'errore: %v", err)
	}
	if adminRows(t, s) != 0 {
		t.Fatal("non doveva creare nulla")
	}
}

// ChangePassword dopo il bootstrap toglie must_change.
func TestBootstrapThenChangePasswordClearsMustChange(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	s := users.New(pool, nil)
	ctx := context.Background()
	if _, err := s.BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: initialPw}); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, users.ChangePasswordInput{Username: "admin", CurrentPassword: initialPw, NewPassword: "nuova password molto lunga"}); err != nil {
		t.Fatal(err)
	}
	u, _ := s.Get(ctx, "admin")
	if must, _ := s.MustChangePassword(ctx, u.ID); must {
		t.Fatal("must_change ancora true")
	}
}
