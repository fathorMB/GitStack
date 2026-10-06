//go:build integration

package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

const pw = "una password lunga e buona"

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	pool  *pgxpool.Pool
	clock *fakeClock
	users *users.Service
	sess  *sessions.Store
	auth  *auth.Service
}

func newEnv(t *testing.T, cfg loginlimit.Config) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &fakeClock{t: time.Now().UTC().Truncate(time.Microsecond)}
	e := &env{pool: pool, clock: c}
	e.users = users.New(pool, c.Now)
	e.sess = sessions.New(pool, c.Now, time.Hour)
	e.auth = &auth.Service{Users: e.users, Sessions: e.sess, Limiter: loginlimit.New(cfg, c.Now)}
	return e
}

var cfgDefault = loginlimit.Config{MaxPerUser: 3, MaxPerIP: 100, Window: 10 * time.Minute}

func (e *env) mkUser(t *testing.T, name string, admin bool) users.User {
	t.Helper()
	u, err := e.users.Create(context.Background(), users.CreateInput{
		Username: name, Email: name + "@example.com", Password: pw, IsAdmin: admin,
	})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return u
}

func (e *env) login(name, pass, ip string) (auth.LoginResult, error) {
	return e.auth.Login(context.Background(), auth.LoginInput{Login: name, Password: pass, IP: ip, UserAgent: "test"})
}

func TestLoginLogoutSession(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	u := e.mkUser(t, "alice", false)

	// Password salvata solo come hash argon2id.
	var hash string
	if err := e.pool.QueryRow(ctx, `SELECT secret_hash FROM identity.credentials WHERE user_id=$1`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) < 30 || hash[:31] != "$argon2id$v=19$m=19456,t=2,p=1$" {
		t.Fatalf("hash non PHC argon2id: %.40q", hash)
	}

	// Login per username e per email (case-insensitive).
	res, err := e.login("alice", pw, "10.0.0.1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := e.login("ALICE@Example.com", pw, "10.0.0.1"); err != nil {
		t.Fatalf("login via email: %v", err)
	}
	if res.SessionValue == "" || res.User.Username != "alice" {
		t.Fatalf("risultato inatteso: %+v", res.User)
	}

	// Nel DB solo l'hash del valore, mai il valore.
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM identity.sessions WHERE token_hash = decode(encode(sha256(convert_to($1,'UTF8')),'hex'),'hex')`, res.SessionValue).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash SHA-256 del cookie non trovato: n=%d err=%v", n, err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM identity.sessions WHERE encode(token_hash,'escape') = $1 OR user_agent = $1`, res.SessionValue).Scan(&n); err != nil || n != 0 {
		t.Fatalf("valore in chiaro nel DB: n=%d", n)
	}

	// Sessione corrente.
	cur, err := e.auth.Session(ctx, res.SessionValue)
	if err != nil || cur.User.ID != u.ID {
		t.Fatalf("Session: %+v %v", cur, err)
	}
	if !res.Session.ExpiresAt.Equal(e.clock.t.Add(time.Hour)) {
		t.Fatalf("scadenza inattesa: %v", res.Session.ExpiresAt)
	}

	// Cookie: HttpOnly, SameSite=Lax, Path=/; Secure solo se richiesto (HTTPS).
	ck := auth.SessionCookie(res.SessionValue, res.Session.ExpiresAt, true)
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Name != "gst_session" {
		t.Fatalf("attributi cookie errati: %+v", ck)
	}
	if plain := auth.SessionCookie(res.SessionValue, res.Session.ExpiresAt, false); plain.Secure || !plain.HttpOnly {
		t.Fatalf("su HTTP il cookie non deve essere Secure: %+v", plain)
	}

	// Logout revoca la sessione corrente e solo quella.
	other, _ := e.login("alice", pw, "10.0.0.1")
	if err := e.auth.Logout(ctx, res.SessionValue); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := e.auth.Session(ctx, res.SessionValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("sessione ancora valida dopo il logout: %v", err)
	}
	if _, err := e.auth.Session(ctx, other.SessionValue); err != nil {
		t.Fatalf("l'altra sessione doveva restare valida: %v", err)
	}
	if err := e.auth.Logout(ctx, res.SessionValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("secondo logout: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	e := newEnv(t, cfgDefault)
	e.mkUser(t, "alice", false)
	res, _ := e.login("alice", pw, "10.0.0.1")
	e.clock.advance(59 * time.Minute)
	if _, err := e.auth.Session(context.Background(), res.SessionValue); err != nil {
		t.Fatalf("sessione scaduta troppo presto: %v", err)
	}
	e.clock.advance(2 * time.Minute)
	if _, err := e.auth.Session(context.Background(), res.SessionValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("sessione non scaduta: %v", err)
	}
}

func TestSameErrorForWrongPasswordUnknownAndInactive(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 100, MaxPerIP: 100, Window: time.Hour})
	ctx := context.Background()
	e.mkUser(t, "alice", false)
	e.mkUser(t, "bob", true)
	f := false
	if _, err := e.users.Update(ctx, "alice", users.UpdateInput{IsActive: &f}); err != nil {
		t.Fatal(err)
	}
	_, e1 := e.login("bob", "password sbagliata!!", "1.1.1.1")
	_, e2 := e.login("nessuno", "password sbagliata!!", "1.1.1.1")
	_, e3 := e.login("alice", pw, "1.1.1.1") // password giusta ma disattivato
	for i, err := range []error{e1, e2, e3} {
		if err != auth.ErrInvalidCredentials {
			t.Fatalf("caso %d: errore %v, atteso ErrInvalidCredentials identico", i, err)
		}
	}
}

// Tempi: utente inesistente e password errata fanno entrambi una verifica
// argon2id, quindi le mediane restano vicine.
func TestLoginTimingSimilar(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 1000, MaxPerIP: 1000, Window: time.Hour})
	e.mkUser(t, "bob", false)
	measure := func(name string) time.Duration {
		var ds []time.Duration
		for i := 0; i < 7; i++ {
			s := time.Now()
			_, _ = e.login(name, "password sbagliata!!", "1.1.1.1")
			ds = append(ds, time.Since(s))
		}
		for i := range ds { // ordinamento semplice
			for j := i + 1; j < len(ds); j++ {
				if ds[j] < ds[i] {
					ds[i], ds[j] = ds[j], ds[i]
				}
			}
		}
		return ds[len(ds)/2]
	}
	measure("bob") // riscaldamento
	wrong, unknown := measure("bob"), measure("nessuno")
	t.Logf("mediana password errata=%v, utente inesistente=%v", wrong, unknown)
	ratio := float64(unknown) / float64(wrong)
	if ratio < 0.5 || ratio > 2 {
		t.Fatalf("tempi troppo diversi: errata=%v inesistente=%v", wrong, unknown)
	}
}

func TestBruteForceBlockAndUnblock(t *testing.T) {
	e := newEnv(t, cfgDefault) // 3 tentativi, finestra 10 minuti
	e.mkUser(t, "alice", false)
	for i := 0; i < 3; i++ {
		if _, err := e.login("alice", "sbagliata sbagliata", "5.5.5.5"); err != auth.ErrInvalidCredentials {
			t.Fatalf("tentativo %d: %v", i, err)
		}
		e.clock.advance(time.Minute)
	}
	// Bloccato: anche con la password giusta e da un altro IP.
	_, err := e.login("alice", pw, "6.6.6.6")
	var rl *auth.RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("atteso RateLimitedError, ottenuto %v", err)
	}
	// Anche un utente inesistente si blocca allo stesso modo.
	for i := 0; i < 3; i++ {
		_, _ = e.login("fantasma", "sbagliata sbagliata", "7.7.7.7")
	}
	if _, err := e.login("fantasma", "x", "7.7.7.7"); !errors.As(err, &rl) {
		t.Fatalf("utente inesistente non bloccato: %v", err)
	}
	// Sblocco dopo la finestra (clock iniettato, nessuno sleep).
	e.clock.advance(10 * time.Minute)
	if _, err := e.login("alice", pw, "6.6.6.6"); err != nil {
		t.Fatalf("login dopo la finestra: %v", err)
	}
}

func TestBruteForcePerIP(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 100, MaxPerIP: 3, Window: time.Minute})
	e.mkUser(t, "alice", false)
	for i := 0; i < 3; i++ {
		_, _ = e.login("u"+string(rune('a'+i)), "sbagliata sbagliata", "8.8.8.8")
	}
	var rl *auth.RateLimitedError
	if _, err := e.login("alice", pw, "8.8.8.8"); !errors.As(err, &rl) {
		t.Fatalf("IP non bloccato: %v", err)
	}
	if _, err := e.login("alice", pw, "9.9.9.9"); err != nil {
		t.Fatalf("un altro IP non deve essere bloccato: %v", err)
	}
}

func TestChangePasswordRevokesSessions(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	e.mkUser(t, "alice", false)
	s1, _ := e.login("alice", pw, "1.1.1.1")
	s2, _ := e.login("alice", pw, "1.1.1.1")

	if err := e.users.ChangePassword(ctx, users.ChangePasswordInput{Username: "alice", CurrentPassword: "sbagliata sbagliata", NewPassword: "nuova password lunga"}); !errors.Is(err, users.ErrInvalidCredentials) {
		t.Fatalf("password attuale errata: %v", err)
	}
	var ve *users.ValidationError
	if err := e.users.ChangePassword(ctx, users.ChangePasswordInput{Username: "alice", CurrentPassword: pw, NewPassword: "corta"}); !errors.As(err, &ve) {
		t.Fatalf("politica non applicata: %v", err)
	}
	if _, err := e.auth.Session(ctx, s1.SessionValue); err != nil {
		t.Fatalf("i tentativi falliti non devono revocare: %v", err)
	}

	// Risparmiando la sessione del chiamante (s1): revoca le altre.
	keep := s1.Session.ID
	if err := e.users.ChangePassword(ctx, users.ChangePasswordInput{Username: "alice", CurrentPassword: pw, NewPassword: "nuova password lunga", KeepSessionID: &keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Session(ctx, s2.SessionValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("l'altra sessione doveva essere revocata")
	}
	if _, err := e.auth.Session(ctx, s1.SessionValue); err != nil {
		t.Fatal("la sessione del chiamante doveva restare")
	}
	if _, err := e.login("alice", pw, "1.1.1.1"); err != auth.ErrInvalidCredentials {
		t.Fatalf("la vecchia password funziona ancora: %v", err)
	}
	s3, err := e.login("alice", "nuova password lunga", "1.1.1.1")
	if err != nil {
		t.Fatalf("login con la nuova password: %v", err)
	}

	// Senza KeepSessionID: revoca tutte. Un admin non serve la password attuale.
	if err := e.users.ChangePassword(ctx, users.ChangePasswordInput{Username: "alice", Admin: true, NewPassword: "ancora un'altra pass"}); err != nil {
		t.Fatal(err)
	}
	for i, v := range []string{s1.SessionValue, s3.SessionValue} {
		if _, err := e.auth.Session(ctx, v); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("sessione %d ancora valida dopo il cambio password", i)
		}
	}
}

func TestDeactivateRevokesSessionsAndTokens(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	u := e.mkUser(t, "alice", false)
	e.mkUser(t, "root", true)
	s1, _ := e.login("alice", pw, "1.1.1.1")
	if _, err := e.pool.Exec(ctx, `INSERT INTO identity.api_tokens (id, user_id, name, token_hash, token_hint, scopes)
		VALUES (gen_random_uuid(), $1, 't', decode(repeat('ab',32),'hex'), 'abcd', ARRAY['read:user'])`, u.ID); err != nil {
		t.Fatal(err)
	}
	f := false
	got, err := e.users.Update(ctx, "alice", users.UpdateInput{IsActive: &f})
	if err != nil || got.IsActive {
		t.Fatalf("disattivazione: %+v %v", got, err)
	}
	if _, err := e.auth.Session(ctx, s1.SessionValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("sessione ancora valida dopo la disattivazione")
	}
	var revoked bool
	if err := e.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM identity.api_tokens WHERE user_id=$1`, u.ID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("token non revocato: %v %v", revoked, err)
	}
	if _, err := e.login("alice", pw, "1.1.1.1"); err != auth.ErrInvalidCredentials {
		t.Fatalf("utente disattivato può accedere: %v", err)
	}
}

func TestRehashOnLogin(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	u := e.mkUser(t, "alice", false)
	// Hash a parametri vecchi (m=8192,t=1): il login lo riscrive con i correnti.
	oldHash := mustOldHash(t, pw)
	if _, err := e.pool.Exec(ctx, `UPDATE identity.credentials SET secret_hash=$2 WHERE user_id=$1`, u.ID, oldHash); err != nil {
		t.Fatal(err)
	}
	if _, err := e.login("alice", pw, "1.1.1.1"); err != nil {
		t.Fatalf("login con hash vecchio: %v", err)
	}
	var h string
	_ = e.pool.QueryRow(ctx, `SELECT secret_hash FROM identity.credentials WHERE user_id=$1`, u.ID).Scan(&h)
	if password.NeedsRehash(h) {
		t.Fatalf("hash non aggiornato: %.30s", h)
	}
}

func mustOldHash(t *testing.T, pw string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(pw), salt, 1, 8192, 1, 32)
	b := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s", b.EncodeToString(salt), b.EncodeToString(key))
}
