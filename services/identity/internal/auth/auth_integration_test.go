//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/secondfactor"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/totp"
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

// ---- F11: secondo fattore TOTP ----

func totpKeys(ids ...string) []secondfactor.Key {
	keys := make([]secondfactor.Key, 0, len(ids))
	for _, id := range ids {
		k := make([]byte, 32)
		copy(k, id) // chiave deterministica per id, diversa fra id
		for i := len(id); i < 32; i++ {
			k[i] = byte(i) ^ id[0]
		}
		keys = append(keys, secondfactor.Key{ID: id, Key: k})
	}
	return keys
}

// withTOTP monta il secondo fattore sull'env e ritorna il servizio.
func (e *env) withTOTP(t *testing.T, required bool) *secondfactor.Service {
	t.Helper()
	sf := secondfactor.New(e.pool, e.clock.Now, totpKeys("k1"), "GitStack")
	e.auth.Second = sf
	e.auth.RequireAdminTOTP = required
	return sf
}

// enableTOTP attiva il TOTP di u e ritorna il segreto e i codici di recupero.
// Dopo la conferma avanza l'orologio di un passo: il codice di conferma è già
// consumato (anti-replay) e i test partono da un passo nuovo.
func (e *env) enableTOTP(t *testing.T, sf *secondfactor.Service, u users.User) ([]byte, []string) {
	t.Helper()
	ctx := context.Background()
	en, err := sf.Enroll(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.Secret)
	if err != nil {
		t.Fatalf("segreto non base32: %v", err)
	}
	codes, err := sf.Confirm(ctx, u.ID, totp.Code(secret, totp.Step(e.clock.Now())))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	e.clock.advance(totp.Period * time.Second)
	return secret, codes
}

func (e *env) loginWith(name, pass, code, recovery string) (auth.LoginResult, error) {
	return e.auth.Login(context.Background(), auth.LoginInput{
		Login: name, Password: pass, TOTPCode: code, RecoveryCode: recovery, IP: "10.0.0.9", UserAgent: "test",
	})
}

func (e *env) sessionCount(t *testing.T, id any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Con il TOTP attivo la password giusta non basta: senza codice niente
// sessione; con un codice sbagliato lo stesso errore di una password
// sbagliata (e conta per il limitatore); col codice giusto si entra. Chi non
// ha il TOTP continua a entrare con la sola password.
func TestLogin_SecondFactorRequired(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	sf := e.withTOTP(t, false)
	alice := e.mkUser(t, "alice", false)
	bob := e.mkUser(t, "bob", false)
	secret, _ := e.enableTOTP(t, sf, alice)

	// Solo password: second_factor_required, nessuna sessione.
	if _, err := e.loginWith("alice", pw, "", ""); !errors.Is(err, auth.ErrSecondFactorRequired) {
		t.Fatalf("senza codice: %v, atteso ErrSecondFactorRequired", err)
	}
	if n := e.sessionCount(t, alice.ID); n != 0 {
		t.Fatalf("sessioni create senza secondo fattore: %d", n)
	}
	// Password sbagliata: resta invalid_credentials (il secondo fattore non
	// si rivela a chi non conosce la password).
	if _, err := e.loginWith("alice", "sbagliata sbagliata", "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("password sbagliata: %v", err)
	}
	// Codice sbagliato: invalid_credentials, conta come tentativo fallito.
	if _, err := e.loginWith("alice", pw, "000000", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("codice sbagliato: %v", err)
	}
	if n := e.sessionCount(t, alice.ID); n != 0 {
		t.Fatalf("sessioni create con codice sbagliato: %d", n)
	}
	// Codice giusto: sessione aperta.
	res, err := e.loginWith("alice", pw, totp.Code(secret, totp.Step(e.clock.Now())), "")
	if err != nil {
		t.Fatalf("login con il codice giusto: %v", err)
	}
	if res.SessionValue == "" || res.MustEnrollTotp {
		t.Fatalf("risultato inatteso: %+v", res)
	}
	// Chi non ha il TOTP entra con la sola password.
	if _, err := e.loginWith("bob", pw, "", ""); err != nil {
		t.Fatalf("login senza TOTP: %v", err)
	}
	_ = bob

	// Il segreto non è in chiaro nel database: cifrato, con l'id della chiave.
	var blob []byte
	var keyID string
	if err := e.pool.QueryRow(ctx, `SELECT secret_enc, enc_key_id FROM identity.totp_credentials WHERE user_id=$1`, alice.ID).Scan(&blob, &keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != "k1" || len(blob) < 12+16+len(secret) || bytes.Contains(blob, secret) {
		t.Fatalf("segreto non cifrato correttamente: key=%q len=%d", keyID, len(blob))
	}
}

// Anti-replay: un codice già usato non vale una seconda volta, e nemmeno un
// codice di un passo precedente a quello già usato; la finestra è ±1.
func TestLogin_TOTPReplayAndWindow(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 50, MaxPerIP: 500, Window: 10 * time.Minute})
	sf := e.withTOTP(t, false)
	alice := e.mkUser(t, "alice", false)
	secret, _ := e.enableTOTP(t, sf, alice)
	step := totp.Step(e.clock.Now())

	// Fuori dalla finestra (+2 passi): rifiutato.
	if _, err := e.loginWith("alice", pw, totp.Code(secret, step+2), ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("codice a +2 passi: %v", err)
	}
	// Nella finestra (passo successivo, +1): accettato.
	code := totp.Code(secret, step+1)
	if _, err := e.loginWith("alice", pw, code, ""); err != nil {
		t.Fatalf("codice a +1 passo: %v", err)
	}
	// Replay dello stesso codice: rifiutato.
	if _, err := e.loginWith("alice", pw, code, ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("replay: %v, atteso ErrInvalidCredentials", err)
	}
	// Un codice del passo corrente (precedente a quello già usato): rifiutato.
	if _, err := e.loginWith("alice", pw, totp.Code(secret, step), ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("codice di un passo già superato: %v", err)
	}
	// Due richieste concorrenti con lo stesso codice nuovo: ne passa una sola.
	e.clock.advance(2 * totp.Period * time.Second)
	fresh := totp.Code(secret, totp.Step(e.clock.Now()))
	var wg sync.WaitGroup
	var okCount int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.loginWith("alice", pw, fresh, ""); err == nil {
				atomic.AddInt32(&okCount, 1)
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("login concorrenti riusciti con lo stesso codice: %d, atteso 1", okCount)
	}
}

// Codici di recupero: dieci, monouso, solo hash nel database, accettati senza
// badare a maiuscole e trattini; non valgono per chi non ha il TOTP.
func TestLogin_RecoveryCodes(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 50, MaxPerIP: 500, Window: 10 * time.Minute})
	ctx := context.Background()
	sf := e.withTOTP(t, false)
	alice := e.mkUser(t, "alice", false)
	_, codes := e.enableTOTP(t, sf, alice)
	if len(codes) != 10 {
		t.Fatalf("codici di recupero: %d, attesi 10", len(codes))
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM identity.totp_recovery_codes WHERE user_id=$1 AND code_hash LIKE '$argon2id$%'`, alice.ID).Scan(&n); err != nil || n != 10 {
		t.Fatalf("hash dei codici: n=%d err=%v", n, err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM identity.totp_recovery_codes WHERE code_hash = ANY($1)`, codes).Scan(&n); err != nil || n != 0 {
		t.Fatalf("codice in chiaro nel database: n=%d err=%v", n, err)
	}

	// Primo uso (scritto in minuscolo e senza trattino): accettato.
	typed := strings.ToLower(strings.ReplaceAll(codes[0], "-", ""))
	if _, err := e.loginWith("alice", pw, "", typed); err != nil {
		t.Fatalf("codice di recupero: %v", err)
	}
	// Secondo uso dello stesso codice: rifiutato.
	if _, err := e.loginWith("alice", pw, "", codes[0]); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("riuso del codice di recupero: %v", err)
	}
	// Un altro codice vale ancora.
	if _, err := e.loginWith("alice", pw, "", codes[1]); err != nil {
		t.Fatalf("secondo codice di recupero: %v", err)
	}
	st, err := sf.Status(ctx, alice.ID)
	if err != nil || !st.Enabled || st.RecoveryCodesRemaining != 8 {
		t.Fatalf("stato: %+v %v, attesi 8 codici rimasti", st, err)
	}
	// Un codice inventato: rifiutato.
	if _, err := e.loginWith("alice", pw, "", "AAAAA-AAAAA"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("codice inventato: %v", err)
	}
}

// Attivazione: in due tempi, ripetibile finché non è confermata, mai doppia.
func TestSecondFactor_EnrollRules(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	sf := e.withTOTP(t, false)
	alice := e.mkUser(t, "alice", false)

	if _, err := sf.Confirm(ctx, alice.ID, "123456"); !errors.Is(err, secondfactor.ErrNotPending) {
		t.Fatalf("Confirm senza Enroll: %v", err)
	}
	first, err := sf.Enroll(ctx, alice.ID, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.URI, "otpauth://totp/GitStack:") || !strings.Contains(first.URI, "secret="+first.Secret) {
		t.Fatalf("URI inatteso: %s", first.URI)
	}
	// In attesa non è un secondo fattore: il login resta con la password.
	if _, err := e.loginWith("alice", pw, "", ""); err != nil {
		t.Fatalf("login con attivazione in attesa: %v", err)
	}
	// Ripetere Enroll sostituisce il segreto in attesa.
	second, err := sf.Enroll(ctx, alice.ID, "alice@example.com")
	if err != nil || second.Secret == first.Secret {
		t.Fatalf("Enroll ripetuto: %v (stesso segreto: %v)", err, second.Secret == first.Secret)
	}
	if _, err := sf.Confirm(ctx, alice.ID, "000000"); !errors.Is(err, secondfactor.ErrInvalidCode) {
		t.Fatalf("Confirm con codice sbagliato: %v", err)
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(second.Secret)
	if _, err := sf.Confirm(ctx, alice.ID, totp.Code(secret, totp.Step(e.clock.Now()))); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := sf.Enroll(ctx, alice.ID, "alice"); !errors.Is(err, secondfactor.ErrAlreadyEnabled) {
		t.Fatalf("Enroll con TOTP attivo: %v", err)
	}
	// Senza chiavi non si attiva.
	none := secondfactor.New(e.pool, e.clock.Now, nil, "")
	if _, err := none.Enroll(ctx, e.mkUser(t, "carol", false).ID, "carol"); !errors.Is(err, secondfactor.ErrUnavailable) {
		t.Fatalf("Enroll senza chiavi: %v", err)
	}
	// Rimozione: poi si rientra con la sola password.
	if err := sf.Remove(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.loginWith("alice", pw, "", ""); err != nil {
		t.Fatalf("login dopo la rimozione: %v", err)
	}
}

// Keyring: un segreto cifrato con k1 si legge con [k2,k1] (rotazione) e non
// con [k2]; un blob non vale per un altro utente (AAD).
func TestSecondFactor_KeyringRotationAndAAD(t *testing.T) {
	e := newEnv(t, loginlimit.Config{MaxPerUser: 50, MaxPerIP: 500, Window: 10 * time.Minute})
	ctx := context.Background()
	sf := e.withTOTP(t, false)
	alice := e.mkUser(t, "alice", false)
	bob := e.mkUser(t, "bob", false)
	secret, _ := e.enableTOTP(t, sf, alice)

	rotated := secondfactor.New(e.pool, e.clock.Now, totpKeys("k2", "k1"), "")
	ok, err := rotated.Verify(ctx, alice.ID, totp.Code(secret, totp.Step(e.clock.Now())), "")
	if err != nil || !ok {
		t.Fatalf("verifica dopo la rotazione: ok=%v err=%v", ok, err)
	}
	lost := secondfactor.New(e.pool, e.clock.Now, totpKeys("k2"), "")
	if _, err := lost.Verify(ctx, alice.ID, "123456", ""); err == nil {
		t.Fatal("la verifica senza la chiave k1 doveva fallire")
	}
	// Il blob di alice spostato su bob non si decifra (AAD con l'id utente).
	if _, err := e.pool.Exec(ctx, `INSERT INTO identity.totp_credentials (user_id, secret_enc, enc_key_id, confirmed_at)
		SELECT $2, secret_enc, enc_key_id, now() FROM identity.totp_credentials WHERE user_id = $1`, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.Verify(ctx, bob.ID, "123456", ""); err == nil {
		t.Fatal("il segreto di un altro utente doveva risultare non valido")
	}
}

// Obbligatorio per gli admin: la sessione a password di un admin senza TOTP
// porta MustEnrollTotp finché non lo attiva; gli altri utenti no.
func TestSecondFactor_RequiredForAdmins(t *testing.T) {
	e := newEnv(t, cfgDefault)
	ctx := context.Background()
	sf := e.withTOTP(t, true)
	admin := e.mkUser(t, "root", true)
	user := e.mkUser(t, "alice", false)

	res, err := e.loginWith("root", pw, "", "")
	if err != nil || !res.MustEnrollTotp {
		t.Fatalf("login admin: %+v %v, atteso MustEnrollTotp", res, err)
	}
	// Una sessione OIDC dello stesso admin non è obbligata: l'MFA è del provider.
	oidcValue, _, err := e.sess.Create(ctx, admin.ID, "oidc", "test", "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if cur, err := e.auth.Session(ctx, oidcValue); err != nil || cur.MustEnrollTotp {
		t.Fatalf("sessione OIDC: %+v %v, non deve richiedere il TOTP", cur, err)
	}
	cur, err := e.auth.Session(ctx, res.SessionValue)
	if err != nil || !cur.MustEnrollTotp {
		t.Fatalf("Session admin: %+v %v", cur, err)
	}
	res, err = e.loginWith("alice", pw, "", "")
	if err != nil || res.MustEnrollTotp {
		t.Fatalf("login utente: %+v %v", res, err)
	}

	secret, _ := e.enableTOTP(t, sf, admin)
	res, err = e.loginWith("root", pw, totp.Code(secret, totp.Step(e.clock.Now())), "")
	if err != nil || res.MustEnrollTotp {
		t.Fatalf("login admin con TOTP: %+v %v", res, err)
	}
	_ = user
}
