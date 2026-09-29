//go:build integration

package apitokens_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/tokens"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type env struct {
	pool  *pgxpool.Pool
	clock *clock
	svc   *apitokens.Service
	users *users.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	return &env{pool: pool, clock: c, svc: apitokens.New(pool, c.now, 30*24*time.Hour), users: users.New(pool, c.now)}
}

func (e *env) user(t *testing.T, name string) uuid.UUID {
	t.Helper()
	u, err := e.users.Create(context.Background(), users.CreateInput{Username: name, Email: name + "@example.com", DisplayName: name, Password: "una password lunga e buona"})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u.ID
}

func (e *env) create(t *testing.T, uid uuid.UUID, name string, days int) (string, apitokens.Token) {
	t.Helper()
	exp := e.clock.now().Add(time.Duration(days) * 24 * time.Hour)
	plain, tok, err := e.svc.Create(context.Background(), apitokens.CreateInput{UserID: uid, Name: name, Scopes: []string{"write:user", "read:org"}, ExpiresAt: &exp})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return plain, tok
}

func TestCreateStoresOnlyHash(t *testing.T) {
	e := newEnv(t)
	uid := e.user(t, "alice")
	plain, tok := e.create(t, uid, "ci", 7)
	if !tokens.Validate(plain) || tok.Hint != plain[len(plain)-4:] {
		t.Fatalf("token o hint incoerenti: %q %q", plain, tok.Hint)
	}
	if got := strings.Join(tok.Scopes, ","); got != "read:org,write:user" {
		t.Fatalf("scope: %s", got)
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT token_hash FROM identity.api_tokens WHERE id = $1`, tok.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	h := tokens.HashBytes(plain)
	if len(stored) != 32 || !tokens.CompareToken(plain, stored) || string(stored) != string(h[:]) {
		t.Fatalf("l'hash salvato non è SHA-256 grezzo del token")
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.api_tokens WHERE position($1 in token_hint) > 0 AND octet_length(token_hash) <> 32`, plain).Scan(&n)
	if n != 0 {
		t.Fatal("riga anomala")
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	uid := e.user(t, "alice")
	now := e.clock.now()
	at := func(d time.Duration) *time.Time { x := now.Add(d); return &x }
	cases := []struct {
		name  string
		in    apitokens.CreateInput
		field string
	}{
		{"senza scadenza", apitokens.CreateInput{Name: "a", Scopes: []string{"read:user"}}, "expiresAt"},
		{"scadenza nel passato", apitokens.CreateInput{Name: "a", Scopes: []string{"read:user"}, ExpiresAt: at(-time.Hour)}, "expiresAt"},
		{"oltre il massimo", apitokens.CreateInput{Name: "a", Scopes: []string{"read:user"}, ExpiresAt: at(31 * 24 * time.Hour)}, "expiresAt"},
		{"scope sconosciuto", apitokens.CreateInput{Name: "a", Scopes: []string{"root"}, ExpiresAt: at(time.Hour)}, "scopes"},
		{"scope duplicato", apitokens.CreateInput{Name: "a", Scopes: []string{"read:user", "read:user"}, ExpiresAt: at(time.Hour)}, "scopes"},
		{"senza scope", apitokens.CreateInput{Name: "a", ExpiresAt: at(time.Hour)}, "scopes"},
		{"nome vuoto", apitokens.CreateInput{Name: "", Scopes: []string{"read:user"}, ExpiresAt: at(time.Hour)}, "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.UserID = uid
			_, _, err := e.svc.Create(context.Background(), c.in)
			var ve *apitokens.ValidationError
			if !errors.As(err, &ve) || ve.Fields[c.field] == "" {
				t.Fatalf("atteso ValidationError su %s, ottenuto %v", c.field, err)
			}
		})
	}
	// Il massimo esatto è accettato.
	if _, _, err := e.svc.Create(context.Background(), apitokens.CreateInput{UserID: uid, Name: "max", Scopes: []string{"read:user"}, ExpiresAt: at(30 * 24 * time.Hour)}); err != nil {
		t.Fatalf("scadenza al massimo: %v", err)
	}
}

func TestDuplicateName(t *testing.T) {
	e := newEnv(t)
	a, b := e.user(t, "alice"), e.user(t, "bob")
	e.create(t, a, "ci", 1)
	exp := e.clock.now().Add(time.Hour)
	_, _, err := e.svc.Create(context.Background(), apitokens.CreateInput{UserID: a, Name: "ci", Scopes: []string{"read:user"}, ExpiresAt: &exp})
	var ne *apitokens.NameInUseError
	if !errors.As(err, &ne) {
		t.Fatalf("atteso NameInUseError, ottenuto %v", err)
	}
	e.create(t, b, "ci", 1) // lo stesso nome su un altro utente va bene
}

func TestVerifyUpdatesLastUsedOncePerMinute(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user(t, "alice")
	plain, tok := e.create(t, uid, "ci", 7)
	p, err := e.svc.Verify(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if p.Username != "alice" || p.Token.ID != tok.ID || len(p.Token.Scopes) != 2 {
		t.Fatalf("principal: %+v", p)
	}
	first := e.clock.now()
	if p.Token.LastUsedAt == nil || !p.Token.LastUsedAt.Equal(first) {
		t.Fatalf("last_used_at non aggiornato: %v", p.Token.LastUsedAt)
	}
	e.clock.t = first.Add(30 * time.Second)
	if _, err := e.svc.Verify(ctx, plain); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	_ = e.pool.QueryRow(ctx, `SELECT last_used_at FROM identity.api_tokens WHERE id = $1`, tok.ID).Scan(&got)
	if !got.Equal(first) {
		t.Fatalf("last_used_at riscritto entro un minuto: %v", got)
	}
	e.clock.t = first.Add(61 * time.Second)
	if _, err := e.svc.Verify(ctx, plain); err != nil {
		t.Fatal(err)
	}
	_ = e.pool.QueryRow(ctx, `SELECT last_used_at FROM identity.api_tokens WHERE id = $1`, tok.ID).Scan(&got)
	if !got.Equal(e.clock.t) {
		t.Fatalf("last_used_at non riscritto dopo un minuto: %v", got)
	}
}

func TestVerifyRejects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	uid := e.user(t, "alice")

	// Sconosciuto ma di formato valido.
	other, err := tokens.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Verify(ctx, other); !errors.Is(err, apitokens.ErrInactive) {
		t.Fatalf("sconosciuto: %v", err)
	}
	if _, err := e.svc.Verify(ctx, "gst_no"); !errors.Is(err, apitokens.ErrInactive) {
		t.Fatalf("formato errato: %v", err)
	}

	// Scaduto.
	plain, _ := e.create(t, uid, "exp", 1)
	e.clock.t = e.clock.t.Add(24*time.Hour + time.Second)
	if _, err := e.svc.Verify(ctx, plain); !errors.Is(err, apitokens.ErrInactive) {
		t.Fatalf("scaduto: %v", err)
	}

	// Revocato.
	plain2, tok2 := e.create(t, uid, "rev", 5)
	if _, err := e.svc.Verify(ctx, plain2); err != nil {
		t.Fatalf("prima della revoca: %v", err)
	}
	if err := e.svc.Revoke(ctx, uid, tok2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Verify(ctx, plain2); !errors.Is(err, apitokens.ErrInactive) {
		t.Fatalf("revocato: %v", err)
	}

	// Utente disattivato.
	plain3, _ := e.create(t, uid, "dis", 5)
	if _, err := e.pool.Exec(ctx, `UPDATE identity.users SET is_active = false WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Verify(ctx, plain3); !errors.Is(err, apitokens.ErrInactive) {
		t.Fatalf("utente disattivato: %v", err)
	}
}

func TestRevokeAndList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user(t, "alice"), e.user(t, "bob")
	_, t1 := e.create(t, a, "uno", 5)
	e.clock.t = e.clock.t.Add(time.Second)
	_, t2 := e.create(t, a, "due", 5)

	// Un altro utente non può revocare, e non sa se esiste.
	if err := e.svc.Revoke(ctx, b, t1.ID); !errors.Is(err, apitokens.ErrNotFound) {
		t.Fatalf("revoca altrui: %v", err)
	}
	if err := e.svc.Revoke(ctx, a, t1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Revoke(ctx, a, t1.ID); !errors.Is(err, apitokens.ErrNotFound) {
		t.Fatalf("seconda revoca: %v", err)
	}
	list, total, err := e.svc.List(ctx, a, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != t2.ID {
		t.Fatalf("elenco: %v %d %+v", err, total, list)
	}
	if l, n, _ := e.svc.List(ctx, b, 1, 20); n != 0 || len(l) != 0 {
		t.Fatal("bob vede token di alice")
	}
	// Il nome di un token revocato si può riusare.
	e.create(t, a, "uno", 5)
}
