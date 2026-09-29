// Package auth implementa il login con password, il logout e la lettura
// della sessione corrente (POST /auth/login, POST /auth/logout,
// GET /auth/session del contratto), componendo users, sessions, password e
// loginlimit.
//
// Garanzie:
//   - utente inesistente, password errata, utente disattivato e utente senza
//     password danno lo stesso errore (ErrInvalidCredentials) e fanno tutti
//     una verifica argon2id (vera o su un hash finto), quindi i tempi restano
//     simili;
//   - i tentativi falliti sono limitati per utente e per IP (loginlimit): a
//     limite raggiunto Login risponde *RateLimitedError prima di toccare
//     database e hash;
//   - password, hash e valore del cookie non compaiono mai in log o errori.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// CookieName è il nome del cookie di sessione.
const CookieName = "gst_session"

// ErrInvalidCredentials è l'unica risposta a ogni tipo di credenziale
// sbagliata (401 invalid_credentials).
var ErrInvalidCredentials = errors.New("credenziali non valide")

// ErrUnauthenticated: nessuna sessione valida (401 unauthenticated).
var ErrUnauthenticated = errors.New("non autenticato")

// RateLimitedError: troppi tentativi falliti (429), da riprovare dopo RetryAfter.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "troppi tentativi di accesso" }

// Service è il servizio di autenticazione.
type Service struct {
	Users    *users.Service
	Sessions *sessions.Store
	Limiter  *loginlimit.Limiter
}

// LoginInput sono i dati di un tentativo di login. Login è username o email.
type LoginInput struct {
	Login     string
	Password  string
	IP        string
	UserAgent string
}

// LoginResult è l'esito di un login riuscito. SessionValue è il valore da
// mettere nel cookie (SessionCookie), da non salvare né loggare.
type LoginResult struct {
	User         users.User
	Session      sessions.Session
	SessionValue string
}

// Login verifica le credenziali e apre una sessione.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	if ok, retry := s.Limiter.Check(in.Login, in.IP); !ok {
		return LoginResult{}, &RateLimitedError{RetryAfter: retry}
	}

	acc, err := s.Users.FindForLogin(ctx, in.Login)
	switch {
	case errors.Is(err, users.ErrNotFound):
		password.VerifyDummy(in.Password)
		return s.fail(in)
	case err != nil:
		return LoginResult{}, err
	}
	if acc.PasswordHash == "" {
		password.VerifyDummy(in.Password)
		return s.fail(in)
	}
	ok, verr := password.Verify(in.Password, acc.PasswordHash)
	if verr != nil {
		// Hash salvato malformato: per il chiamante è come una password sbagliata.
		password.VerifyDummy(in.Password)
		return s.fail(in)
	}
	if !ok || !acc.User.IsActive {
		return s.fail(in)
	}

	s.Limiter.Success(in.Login)
	if password.NeedsRehash(acc.PasswordHash) {
		_ = s.Users.UpgradeHash(ctx, acc.User.ID, in.Password) // best effort
	}
	value, sess, err := s.Sessions.Create(ctx, acc.User.ID, "password", in.UserAgent, in.IP)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: acc.User, Session: sess, SessionValue: value}, nil
}

func (s *Service) fail(in LoginInput) (LoginResult, error) {
	s.Limiter.Fail(in.Login, in.IP)
	return LoginResult{}, ErrInvalidCredentials
}

// Logout revoca la sessione del cookie. Senza sessione valida: ErrUnauthenticated.
func (s *Service) Logout(ctx context.Context, sessionValue string) error {
	ok, err := s.Sessions.Revoke(ctx, sessionValue)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnauthenticated
	}
	return nil
}

// Current è la sessione corrente con il suo utente.
type Current struct {
	User    users.User
	Session sessions.Session
}

// Session risolve il valore del cookie nella sessione e nell'utente
// corrente; ErrUnauthenticated se non valida, scaduta, revocata o utente
// disattivato.
func (s *Service) Session(ctx context.Context, sessionValue string) (Current, error) {
	sess, err := s.Sessions.Lookup(ctx, sessionValue)
	if errors.Is(err, sessions.ErrNotFound) {
		return Current{}, ErrUnauthenticated
	}
	if err != nil {
		return Current{}, err
	}
	u, err := s.Users.GetByID(ctx, sess.UserID)
	if errors.Is(err, users.ErrNotFound) {
		return Current{}, ErrUnauthenticated
	}
	if err != nil {
		return Current{}, err
	}
	return Current{User: u, Session: sess}, nil
}

// SessionCookie costruisce il cookie di sessione: HttpOnly, Secure,
// SameSite=Lax, Path=/, con scadenza uguale a quella della sessione.
func SessionCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires.UTC(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie costruisce il cookie che cancella quello di sessione (logout).
func ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}
