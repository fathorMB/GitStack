//go:build integration

package oidc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pgService(t *testing.T, providers []Provider) (*Service, *pgxpool.Pool, *users.Service) {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	us := users.New(pool, nil)
	store := NewPGStore(pool, us, sessions.New(pool, nil, time.Hour), nil)
	svc, err := New(store, Config{Providers: providers, Key: testKey, KeyID: testKeyID, PublicURL: "https://git.test"},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc, pool, us
}

func provider(slug, issuer string) Provider {
	return Provider{
		Slug: slug, DisplayName: slug, Issuer: issuer, ClientID: testClientID, ClientSecret: testClientSecret,
		Scopes: []string{"openid", "profile", "email"},
		Claims: Claims{Email: DefaultClaimEmail, EmailVerified: DefaultClaimEmailVerified,
			Username: DefaultClaimUsername, DisplayName: DefaultClaimDisplayName},
	}
}

func TestPG_SyncProviders(t *testing.T) {
	ctx := context.Background()
	a, b := provider("uno", "https://uno.example"), provider("due", "https://due.example")
	svc, pool, _ := pgService(t, []Provider{a, b})
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	var enc []byte
	var keyID string
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT client_secret_enc, enc_key_id, enabled FROM identity.oidc_providers WHERE slug='uno'`).Scan(&enc, &keyID, &enabled); err != nil {
		t.Fatal(err)
	}
	if string(enc) == testClientSecret || keyID != testKeyID || !enabled {
		t.Errorf("riga inattesa: enc=%q key=%q enabled=%v", enc, keyID, enabled)
	}
	if got, err := DecryptSecret(testKey, enc); err != nil || got != testClientSecret {
		t.Errorf("decifratura: %q %v", got, err)
	}
	var id1 string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM identity.oidc_providers WHERE slug='uno'`).Scan(&id1)

	// Secondo avvio: upsert per slug (stesso id), campi aggiornati.
	a.DisplayName, a.Issuer = "Uno bis", "https://uno2.example"
	svc.cfg.Providers = []Provider{a}
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var id2, name, issuer string
	_ = pool.QueryRow(ctx, `SELECT id::text, display_name, issuer_url FROM identity.oidc_providers WHERE slug='uno'`).Scan(&id2, &name, &issuer)
	if id1 != id2 || name != "Uno bis" || issuer != "https://uno2.example" {
		t.Errorf("upsert: id %s→%s name=%q issuer=%q", id1, id2, name, issuer)
	}
	// "due" è tolto dal file: disabilitato, non cancellato.
	if err := pool.QueryRow(ctx, `SELECT enabled FROM identity.oidc_providers WHERE slug='due'`).Scan(&enabled); err != nil || enabled {
		t.Errorf("due doveva essere disabilitato: enabled=%v err=%v", enabled, err)
	}
	store := svc.repo.(*PGStore)
	if _, err := store.Provider(ctx, "due"); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Provider(due) = %v", err)
	}
	// Rimesso nel file: torna abilitato.
	svc.cfg.Providers = []Provider{a, b}
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Provider(ctx, "due"); err != nil {
		t.Errorf("Provider(due) dopo il reinserimento: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity.oidc_providers`).Scan(&n)
	if n != 2 {
		t.Errorf("righe = %d, volute 2", n)
	}
}

func TestPG_LinkAndCreate(t *testing.T) {
	ctx := context.Background()
	idp := newIdP(t)
	p := provider("kc", idp.srv.URL)
	p.LinkByVerifiedEmail, p.AutoCreateUsers = true, true
	svc, pool, us := pgService(t, []Provider{p})
	svc.HTTPClient = idp.srv.Client()
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, idp: idp, svc: svc, clock: &clock{t: time.Now()}}

	existing, err := us.Create(ctx, users.CreateInput{Username: "alice-locale", Email: "Alice@example.com", DisplayName: "A", Password: "una password lunga e buona"})
	if err != nil {
		t.Fatal(err)
	}

	// 1. email verificata: collega all'utente esistente, sessione 'oidc'.
	res, err := r.finish(r.begin("/x"))
	if err != nil || res.User.ID != existing.ID {
		t.Fatalf("collegamento per email: %v %+v", err, res.User)
	}
	var method string
	if err := pool.QueryRow(ctx, `SELECT auth_method FROM identity.sessions WHERE id=$1`, res.Session.ID).Scan(&method); err != nil || method != "oidc" {
		t.Errorf("auth_method = %q %v", method, err)
	}
	var last *time.Time
	_ = pool.QueryRow(ctx, `SELECT last_login_at FROM identity.oidc_identities WHERE subject='sub-1'`).Scan(&last)
	if last == nil {
		t.Error("last_login_at non impostato")
	}

	// 2. secondo login: stesso utente via (provider, subject), anche se l'email cambia.
	idp.claims = func(c map[string]any, _ authCode) { c["email"] = "altra@example.com" }
	res, err = r.finish(r.begin(""))
	if err != nil || res.User.ID != existing.ID {
		t.Fatalf("secondo login: %v", err)
	}
	var email *string
	_ = pool.QueryRow(ctx, `SELECT email FROM identity.oidc_identities WHERE subject='sub-1'`).Scan(&email)
	if email == nil || *email != "altra@example.com" {
		t.Errorf("email dell'identità non aggiornata: %v", email)
	}

	// 3. altro subject con email non verificata di un utente esistente: 409.
	idp.claims = func(c map[string]any, _ authCode) {
		c["sub"], c["email"], c["email_verified"] = "sub-2", "alice@example.com", false
	}
	if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUnlinked) {
		t.Fatalf("email non verificata: err = %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity.oidc_identities WHERE subject='sub-2'`).Scan(&n)
	if n != 0 {
		t.Error("identità non verificata collegata")
	}

	// 4. altro subject nuovo, verificato: utente creato senza password.
	idp.claims = func(c map[string]any, _ authCode) {
		c["sub"], c["email"], c["preferred_username"] = "sub-3", "carol@example.com", "alice-locale"
	}
	res, err = r.finish(r.begin(""))
	if err != nil {
		t.Fatal(err)
	}
	if res.User.Username != "alice-locale-2" || res.User.Email == nil || *res.User.Email != "carol@example.com" {
		t.Errorf("utente creato: %+v", res.User)
	}
	acc, err := us.FindForLogin(ctx, "carol@example.com")
	if err != nil || acc.PasswordHash != "" {
		t.Errorf("l'utente OIDC non deve avere password: %v hash=%q", err, acc.PasswordHash)
	}

	// 5. utente disattivato: nessuna sessione.
	inactive := false
	if _, err := us.Update(ctx, "alice-locale-2", users.UpdateInput{IsActive: &inactive}); err != nil {
		t.Fatal(err)
	}
	var before, after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity.sessions`).Scan(&before)
	if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUserInactive) {
		t.Fatalf("utente disattivato: err = %v", err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity.sessions`).Scan(&after)
	if before != after {
		t.Error("sessione creata per un utente disattivato")
	}

	// 6. le identità spariscono con l'utente (FK ON DELETE CASCADE).
	if err := us.Delete(ctx, "alice-locale-2"); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM identity.oidc_identities WHERE subject='sub-3'`).Scan(&n)
	if n != 0 {
		t.Error("identità rimasta dopo l'eliminazione dell'utente")
	}
}
