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
//   - con il TOTP attivo (F11) la password giusta non basta: senza codice
//     Login risponde ErrSecondFactorRequired e non apre nessuna sessione; un
//     codice sbagliato conta come tentativo fallito (ErrInvalidCredentials);
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
	"github.com/google/uuid"
)

// CookieName è il nome storico del cookie di sessione (servizio in HTTP).
const CookieName = "gst_session"

// HostCookieName è il nome con prefisso __Host- usato quando la richiesta è
// HTTPS (F16, V3.4.4): il browser lo accetta solo Secure, Path=/ e senza
// Domain, quindi un sottodominio non può impostarlo.
const HostCookieName = "__Host-gst_session"

// SessionValue legge il valore del cookie di sessione: prima quello con
// prefisso __Host-, poi il nome storico (le sessioni aperte prima del cambio
// restano valide).
func SessionValue(r *http.Request) (string, bool) {
	for _, name := range []string{HostCookieName, CookieName} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value, true
		}
	}
	return "", false
}

// ErrInvalidCredentials è l'unica risposta a ogni tipo di credenziale
// sbagliata (401 invalid_credentials).
var ErrInvalidCredentials = errors.New("credenziali non valide")

// ErrSecondFactorRequired: password giusta ma serve il secondo fattore
// (401 second_factor_required).
var ErrSecondFactorRequired = errors.New("secondo fattore richiesto")

// SecondFactor è la parte del servizio TOTP usata dal login.
type SecondFactor interface {
	// Enabled: l'utente ha un TOTP confermato.
	Enabled(ctx context.Context, userID uuid.UUID) (bool, error)
	// Verify controlla un codice TOTP o un codice di recupero (si consuma).
	Verify(ctx context.Context, userID uuid.UUID, totpCode, recoveryCode string) (bool, error)
}

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
	// Second è il secondo fattore TOTP; nil lo spegne del tutto.
	Second SecondFactor
	// RequireAdminTOTP: gli amministratori con sessione a password devono
	// avere il TOTP attivo (Current.MustEnrollTotp finché non lo attivano).
	RequireAdminTOTP bool
}

// LoginInput sono i dati di un tentativo di login. Login è username o email.
type LoginInput struct {
	Login    string
	Password string
	// TOTPCode e RecoveryCode: secondo passo del login (uno dei due).
	TOTPCode     string
	RecoveryCode string
	IP           string
	UserAgent    string
}

// LoginResult è l'esito di un login riuscito. SessionValue è il valore da
// mettere nel cookie (SessionCookie), da non salvare né loggare.
type LoginResult struct {
	User         users.User
	Session      sessions.Session
	SessionValue string
	// MustChange: la password va cambiata prima di qualunque altra operazione.
	MustChange bool
	// MustEnrollTotp: amministratore senza TOTP in un'installazione che lo impone.
	MustEnrollTotp bool
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

	if s.Second != nil {
		enabled, err := s.Second.Enabled(ctx, acc.User.ID)
		if err != nil {
			return LoginResult{}, err
		}
		if enabled {
			if in.TOTPCode == "" && in.RecoveryCode == "" {
				return LoginResult{}, ErrSecondFactorRequired
			}
			good, err := s.Second.Verify(ctx, acc.User.ID, in.TOTPCode, in.RecoveryCode)
			if err != nil {
				return LoginResult{}, err
			}
			if !good {
				return s.fail(in)
			}
		}
	}

	s.Limiter.Success(in.Login)
	if password.NeedsRehash(acc.PasswordHash) {
		_ = s.Users.UpgradeHash(ctx, acc.User.ID, in.Password) // best effort
	}
	value, sess, err := s.Sessions.Create(ctx, acc.User.ID, "password", in.UserAgent, in.IP)
	if err != nil {
		return LoginResult{}, err
	}
	must, err := s.Users.MustChangePassword(ctx, acc.User.ID)
	if err != nil {
		return LoginResult{}, err
	}
	enroll, err := s.mustEnroll(ctx, acc.User, "password")
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: acc.User, Session: sess, SessionValue: value, MustChange: must, MustEnrollTotp: enroll}, nil
}

// mustEnroll dice se l'utente deve ancora attivare il TOTP: solo gli
// amministratori con sessione a password, e solo se l'installazione lo impone.
// Token personali e OIDC restano fuori (il secondo fattore dell'OIDC è del provider).
func (s *Service) mustEnroll(ctx context.Context, u users.User, method string) (bool, error) {
	if !s.RequireAdminTOTP || s.Second == nil || !u.IsAdmin || method != "password" {
		return false, nil
	}
	enabled, err := s.Second.Enabled(ctx, u.ID)
	if err != nil {
		return false, err
	}
	return !enabled, nil
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
	// MustChange: la password dell'utente va cambiata prima di altre operazioni.
	MustChange bool
	// MustEnrollTotp: il TOTP va attivato prima di altre operazioni (solo
	// amministratori con sessione a password, installazione che lo impone).
	MustEnrollTotp bool
	// Scopes e TokenID: solo per le richieste con token personale (identità
	// firmata dal gateway); vuoti per le sessioni.
	Scopes  []string
	TokenID string
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
	must, err := s.Users.MustChangePassword(ctx, u.ID)
	if err != nil {
		return Current{}, err
	}
	enroll, err := s.mustEnroll(ctx, u, sess.AuthMethod)
	if err != nil {
		return Current{}, err
	}
	return Current{User: u, Session: sess, MustChange: must, MustEnrollTotp: enroll}, nil
}

// SessionCookie costruisce il cookie di sessione: HttpOnly, SameSite=Lax,
// Path=/, con scadenza uguale a quella della sessione. Secure solo se la
// richiesta originale è HTTPS (secure): su HTTP il browser scarterebbe il
// cookie e il login non funzionerebbe. Con secure il nome è HostCookieName.
func SessionCookie(value string, expires time.Time, secure bool) *http.Cookie {
	name := CookieName
	if secure {
		name = HostCookieName
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires.UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// SessionCookies sono i cookie da impostare dopo un login: quello di sessione e,
// in HTTPS, la cancellazione del cookie storico senza prefisso, così il browser
// non tiene due sessioni.
func SessionCookies(value string, expires time.Time, secure bool) []*http.Cookie {
	cs := []*http.Cookie{SessionCookie(value, expires, secure)}
	if secure {
		cs = append(cs, clearNamed(CookieName, secure))
	}
	return cs
}

// ClearCookies sono i cookie che cancellano la sessione (logout): in HTTPS
// entrambi i nomi, perché una sessione aperta prima del cambio ha il nome
// storico.
func ClearCookies(secure bool) []*http.Cookie {
	if secure {
		return []*http.Cookie{clearNamed(HostCookieName, true), clearNamed(CookieName, true)}
	}
	return []*http.Cookie{clearNamed(CookieName, false)}
}

// ClearCookie costruisce il cookie che cancella quello di sessione (logout).
func ClearCookie(secure bool) *http.Cookie {
	if secure {
		return clearNamed(HostCookieName, true)
	}
	return clearNamed(CookieName, false)
}

func clearNamed(name string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
