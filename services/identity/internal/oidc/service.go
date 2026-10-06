package oidc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const (
	// StateCookieName è il cookie cifrato con lo stato del flusso.
	StateCookieName = "gst_oidc_state"

	// StateTTL è la durata massima fra start e callback.
	StateTTL = 10 * time.Minute

	// CallbackPathFormat è il percorso pubblico della callback (l'ingress
	// espone /api/v1 e lo instrada al gateway come /v1).
	CallbackPathFormat = "/api/v1/auth/oidc/%s/callback"

	maxDiscoveryTime = 15 * time.Second
)

// Errori del flusso, mappati a status HTTP dal livello httpapi.
var (
	ErrInvalidState    = errors.New("stato del login OIDC non valido")         // 400
	ErrStateExpired    = errors.New("stato del login OIDC scaduto")            // 400
	ErrLoginFailed     = errors.New("login OIDC non riuscito")                 // 401
	ErrUserInactive    = errors.New("utente disattivato")                      // 401
	ErrUnlinked        = errors.New("identità OIDC non collegata a un utente") // 409
	ErrDisabled        = errors.New("login OIDC non configurato")              // 404
	ErrInvalidRedirect = errors.New("redirectTo non valido")                   // 400
	errKeyMismatch     = errors.New("la chiave di cifratura non corrisponde al key id del provider")
)

// Config è la configurazione del servizio OIDC.
type Config struct {
	Providers []Provider
	// Key è la chiave AES-256 (32 byte) e KeyID il suo identificativo, salvato
	// in oidc_providers.enc_key_id.
	Key   []byte
	KeyID string
	// PublicURL è l'URL pubblico (senza slash finale) da cui il browser
	// raggiunge GitStack; la callback è PublicURL + CallbackPathFormat.
	PublicURL string
}

// Service esegue il flusso di login.
type Service struct {
	repo Repository
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
	// HTTPClient è usato per discovery, JWKS e token endpoint. Nil: client
	// con timeout di 15 secondi.
	HTTPClient *http.Client

	bySlug map[string]Provider

	mu    sync.Mutex
	disco map[string]*gooidc.Provider
	used  map[string]time.Time // hash dello state già consumato -> scadenza
}

// New crea il servizio. Con nessun provider il login OIDC è spento.
func New(repo Repository, cfg Config, log *slog.Logger, now func() time.Time) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if len(cfg.Providers) > 0 {
		if len(cfg.Key) != 32 {
			return nil, errors.New("la chiave di cifratura OIDC deve essere di 32 byte")
		}
		if cfg.KeyID == "" {
			return nil, errors.New("il key id della chiave OIDC è obbligatorio")
		}
		if _, err := url.Parse(cfg.PublicURL); err != nil || cfg.PublicURL == "" {
			return nil, errors.New("l'URL pubblico di identity è obbligatorio per il login OIDC")
		}
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	s := &Service{
		repo: repo, cfg: cfg, log: log, now: now,
		bySlug: map[string]Provider{}, disco: map[string]*gooidc.Provider{}, used: map[string]time.Time{},
	}
	for _, p := range cfg.Providers {
		s.bySlug[p.Slug] = p
	}
	return s, nil
}

// Sync scrive i provider del file in identity.oidc_providers (segreto
// cifrato) e disabilita quelli tolti.
func (s *Service) Sync(ctx context.Context) error {
	rows := make([]SyncRow, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		enc, err := EncryptSecret(s.cfg.Key, p.ClientSecret)
		if err != nil {
			return fmt.Errorf("cifratura del segreto del provider %q non riuscita: %w", p.Slug, err)
		}
		rows = append(rows, SyncRow{
			Slug: p.Slug, DisplayName: p.DisplayName, Issuer: p.Issuer, ClientID: p.ClientID,
			SecretEnc: enc, EncKeyID: s.cfg.KeyID, Scopes: p.Scopes,
		})
	}
	return s.repo.SyncProviders(ctx, rows)
}

// Info è la parte pubblica di un provider (listOidcProviders).
type Info struct{ Slug, DisplayName string }

// List elenca i provider configurati, nell'ordine del file.
func (s *Service) List() []Info {
	out := make([]Info, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		out = append(out, Info{Slug: p.Slug, DisplayName: p.DisplayName})
	}
	return out
}

// CallbackURL è la redirect_uri registrata presso il provider.
func (s *Service) CallbackURL(slug string) string {
	return s.cfg.PublicURL + fmt.Sprintf(CallbackPathFormat, slug)
}

func (s *Service) callbackPath(slug string) string {
	u, err := url.Parse(s.CallbackURL(slug))
	if err != nil || u.Path == "" {
		return fmt.Sprintf(CallbackPathFormat, slug)
	}
	return u.Path
}

func (s *Service) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: maxDiscoveryTime}
}

func (s *Service) withClient(ctx context.Context) context.Context {
	return gooidc.ClientContext(ctx, s.httpClient())
}

// resolved è un provider pronto all'uso: riga del database, segreto in
// chiaro, discovery e configurazione oauth2.
type resolved struct {
	row  ProviderRow
	conf Provider
	disc *gooidc.Provider
	oa   *oauth2.Config
}

func (s *Service) resolve(ctx context.Context, slug string) (resolved, error) {
	conf, ok := s.bySlug[slug]
	if !ok {
		return resolved{}, ErrDisabled
	}
	row, err := s.repo.Provider(ctx, slug)
	if err != nil {
		return resolved{}, err
	}
	if row.EncKeyID != s.cfg.KeyID {
		return resolved{}, errKeyMismatch
	}
	secret, err := DecryptSecret(s.cfg.Key, row.SecretEnc)
	if err != nil {
		return resolved{}, fmt.Errorf("decifratura del segreto del provider %q non riuscita: %w", slug, err)
	}

	ctx = s.withClient(ctx)
	disc, err := s.discovery(ctx, row.Issuer)
	if err != nil {
		return resolved{}, fmt.Errorf("discovery del provider %q non riuscita: %w", slug, err)
	}
	return resolved{
		row: row, conf: conf, disc: disc,
		oa: &oauth2.Config{
			ClientID: row.ClientID, ClientSecret: secret, Endpoint: disc.Endpoint(),
			RedirectURL: s.CallbackURL(slug), Scopes: row.Scopes,
		},
	}, nil
}

// discovery scarica (una volta) il documento di discovery del provider. Il
// JWKS è tenuto in cache da go-oidc.
func (s *Service) discovery(ctx context.Context, issuer string) (*gooidc.Provider, error) {
	s.mu.Lock()
	d, ok := s.disco[issuer]
	s.mu.Unlock()
	if ok {
		return d, nil
	}
	dctx, cancel := context.WithTimeout(ctx, maxDiscoveryTime)
	defer cancel()
	d, err := gooidc.NewProvider(dctx, issuer)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.disco[issuer] = d
	s.mu.Unlock()
	return d, nil
}

// StartResult è l'esito di Start: URL del provider e cookie di stato.
type StartResult struct {
	URL    string
	Cookie *http.Cookie
}

var redirectRe = regexp.MustCompile(`^/[^/\\].*$|^/$`)

// ValidRedirect dice se redirectTo è un percorso relativo ammesso (pattern
// del contratto, più nessun carattere di controllo).
func ValidRedirect(r string) bool {
	if len(r) > 512 || !redirectRe.MatchString(r) {
		return false
	}
	for _, c := range r {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// Start prepara il redirect al provider (authorization code + PKCE S256,
// state e nonce casuali) e il cookie cifrato con lo stato.
func (s *Service) Start(ctx context.Context, slug, redirectTo string, secure bool) (StartResult, error) {
	if redirectTo == "" {
		redirectTo = "/"
	}
	if !ValidRedirect(redirectTo) {
		return StartResult{}, ErrInvalidRedirect
	}
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return StartResult{}, err
	}
	state, err := randomToken(32)
	if err != nil {
		return StartResult{}, err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return StartResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	exp := s.now().Add(StateTTL)
	value, err := sealState(s.cfg.Key, flowState{
		Slug: slug, State: state, Nonce: nonce, Verifier: verifier, RedirectTo: redirectTo, Expires: exp.Unix(),
	})
	if err != nil {
		return StartResult{}, err
	}
	authURL := r.oa.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), gooidc.Nonce(nonce))
	return StartResult{URL: authURL, Cookie: s.stateCookie(slug, value, int(StateTTL/time.Second), secure)}, nil
}

func (s *Service) stateCookie(slug, value string, maxAge int, secure bool) *http.Cookie {
	c := &http.Cookie{
		Name: StateCookieName, Value: value, Path: s.callbackPath(slug),
		MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	}
	if maxAge < 0 {
		c.Value = ""
		c.Expires = time.Unix(0, 0).UTC()
	}
	return c
}

// ClearStateCookie cancella il cookie di stato (a fine callback, riuscita o no).
func (s *Service) ClearStateCookie(slug string, secure bool) *http.Cookie {
	return s.stateCookie(slug, "", -1, secure)
}

// FinishInput sono i dati della callback.
type FinishInput struct {
	Slug        string
	Code, State string
	// StateCookie è il valore del cookie gst_oidc_state ("" se assente).
	StateCookie string
	UserAgent   string
	IP          string
}

// FinishResult è l'esito di un login riuscito.
type FinishResult struct {
	User         users.User
	Session      sessions.Session
	SessionValue string
	RedirectTo   string
}

// Finish completa il login: verifica cookie e state, scambia il code (con il
// code_verifier), valida il token ID (firma, issuer, audience, scadenza) e il
// nonce, poi collega o crea l'utente e apre la sessione.
func (s *Service) Finish(ctx context.Context, in FinishInput) (FinishResult, error) {
	conf, ok := s.bySlug[in.Slug]
	if !ok {
		return FinishResult{}, ErrDisabled
	}
	if in.StateCookie == "" {
		return FinishResult{}, ErrInvalidState
	}
	st, err := openState(s.cfg.Key, in.StateCookie, s.now())
	if err != nil {
		if errors.Is(err, ErrStateExpired) {
			return FinishResult{}, ErrStateExpired
		}
		return FinishResult{}, ErrInvalidState
	}
	if st.Slug != in.Slug || subtle.ConstantTimeCompare([]byte(st.State), []byte(in.State)) != 1 {
		return FinishResult{}, ErrInvalidState
	}
	if !s.consume(st) {
		return FinishResult{}, ErrInvalidState
	}

	r, err := s.resolve(ctx, in.Slug)
	if err != nil {
		return FinishResult{}, err
	}
	cctx := s.withClient(ctx)
	tok, err := r.oa.Exchange(cctx, in.Code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		s.log.Warn("scambio del code OIDC non riuscito", "provider", in.Slug, "err", safeErr(err))
		return FinishResult{}, ErrLoginFailed
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		s.log.Warn("risposta OIDC senza id_token", "provider", in.Slug)
		return FinishResult{}, ErrLoginFailed
	}
	idt, err := r.disc.Verifier(&gooidc.Config{ClientID: r.row.ClientID, Now: s.now}).Verify(cctx, raw)
	if err != nil {
		s.log.Warn("token ID OIDC non valido", "provider", in.Slug, "err", safeErr(err))
		return FinishResult{}, ErrLoginFailed
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 || st.Nonce == "" {
		s.log.Warn("nonce del token ID OIDC diverso", "provider", in.Slug)
		return FinishResult{}, ErrLoginFailed
	}
	if idt.Subject == "" {
		return FinishResult{}, ErrLoginFailed
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return FinishResult{}, ErrLoginFailed
	}

	id := identity{
		subject:     idt.Subject,
		email:       strings.ToLower(strings.TrimSpace(claimString(claims, conf.Claims.Email))),
		verified:    claimTrue(claims, conf.Claims.EmailVerified),
		username:    claimString(claims, conf.Claims.Username),
		displayName: strings.TrimSpace(claimString(claims, conf.Claims.DisplayName)),
	}
	u, err := s.linkOrCreate(ctx, r, id)
	if err != nil {
		return FinishResult{}, err
	}
	value, sess, err := s.repo.CreateSession(ctx, u.ID, in.UserAgent, in.IP)
	if err != nil {
		return FinishResult{}, err
	}
	return FinishResult{User: u, Session: sess, SessionValue: value, RedirectTo: st.RedirectTo}, nil
}

// consume segna lo state come usato; false se lo era già (riuso del cookie).
func (s *Service) consume(st flowState) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, exp := range s.used {
		if !exp.After(now) {
			delete(s.used, k)
		}
	}
	if _, dup := s.used[st.State]; dup {
		return false
	}
	s.used[st.State] = time.Unix(st.Expires, 0)
	return true
}

// safeErr toglie dall'errore ciò che potrebbe contenere il corpo della
// risposta del provider (code, token): resta il tipo di errore.
func safeErr(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return "oauth2: " + re.ErrorCode + " (HTTP " + strconv.Itoa(re.Response.StatusCode) + ")"
	}
	return err.Error()
}

func claimString(c map[string]any, name string) string {
	if name == "" {
		return ""
	}
	s, _ := c[name].(string)
	return s
}

// claimTrue: vero solo per il booleano true o per la stringa esatta "true".
func claimTrue(c map[string]any, name string) bool {
	if name == "" {
		return false
	}
	switch v := c[name].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

type identity struct {
	subject, email, username, displayName string
	verified                              bool
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && a.Name == "" && len(s) <= 254
}

// linkOrCreate implementa il collegamento a un utente locale:
//  1. (provider, subject) già collegato: login;
//  2. linkByVerifiedEmail ed email verificata (true o "true"): collega
//     all'utente con quella email;
//  3. autoCreateUsers: crea un utente senza password e lo collega (se l'email
//     è già di un utente → ErrUnlinked; l'email si salva solo se verificata);
//  4. altrimenti ErrUnlinked.
//
// Un'email non verificata non collega mai a un utente esistente.
func (s *Service) linkOrCreate(ctx context.Context, r resolved, id identity) (users.User, error) {
	for attempt := 0; attempt < 2; attempt++ {
		u, err := s.linkOnce(ctx, r, id)
		if errors.Is(err, ErrIdentityExists) {
			continue // un'altra callback ha collegato nel frattempo: si rilegge
		}
		return u, err
	}
	return users.User{}, ErrLoginFailed
}

func (s *Service) linkOnce(ctx context.Context, r resolved, id identity) (users.User, error) {
	pid := r.row.ID
	u, found, err := s.repo.IdentityUser(ctx, pid, id.subject)
	if err != nil {
		return users.User{}, err
	}
	if found {
		if !u.IsActive {
			return users.User{}, ErrUserInactive
		}
		if err := s.repo.TouchIdentity(ctx, pid, id.subject, id.email); err != nil {
			return users.User{}, err
		}
		return u, nil
	}

	if r.conf.LinkByVerifiedEmail && id.verified && id.email != "" {
		existing, found, err := s.repo.UserByEmail(ctx, id.email)
		if err != nil {
			return users.User{}, err
		}
		if found {
			if !existing.IsActive {
				return users.User{}, ErrUserInactive
			}
			if err := s.repo.LinkIdentity(ctx, existing.ID, pid, id.subject, id.email); err != nil {
				return users.User{}, err
			}
			return existing, nil
		}
	}

	if !r.conf.AutoCreateUsers {
		return users.User{}, ErrUnlinked
	}
	email := ""
	if id.email != "" {
		if _, taken, err := s.repo.UserByEmail(ctx, id.email); err != nil {
			return users.User{}, err
		} else if taken {
			return users.User{}, ErrUnlinked
		}
		// Un'email non verificata non si assegna all'utente nuovo: potrebbe
		// non essere di chi accede e occuperebbe quella di un utente futuro.
		if id.verified && validEmail(id.email) {
			email = id.email
		}
	}
	return s.create(ctx, pid, id, email)
}

func (s *Service) create(ctx context.Context, providerID uuid.UUID, id identity, email string) (users.User, error) {
	display := id.displayName
	if len([]rune(display)) > 128 {
		display = string([]rune(display)[:128])
	}
	base := id.username
	if base == "" {
		base, _, _ = strings.Cut(id.email, "@")
	}
	base = NormalizeUsername(base)

	for n := 1; n <= 50; n++ {
		name := usernameWithSuffix(base, n)
		u, err := s.repo.CreateUser(ctx, users.CreateInput{
			Username: name, Kind: users.KindHuman, Email: email, DisplayName: display,
		})
		var rn *users.ReservedNameError
		if errors.As(err, &rn) {
			continue // nome riservato: si prova il successivo con suffisso
		}
		var ae *users.AlreadyExistsError
		if errors.As(err, &ae) {
			if ae.Field == "email" {
				return users.User{}, ErrUnlinked
			}
			continue
		}
		if err != nil {
			return users.User{}, err
		}
		if err := s.repo.LinkIdentity(ctx, u.ID, providerID, id.subject, id.email); err != nil {
			// Niente utente orfano se il collegamento fallisce.
			if derr := s.repo.DeleteUser(ctx, u.Username); derr != nil {
				s.log.Error("pulizia dell'utente OIDC non riuscita", "username", u.Username, "err", derr)
			}
			return users.User{}, err
		}
		return u, nil
	}
	return users.User{}, fmt.Errorf("nessun username libero per %q", base)
}

var badUsernameChars = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeUsername porta un valore qualunque al formato Name degli
// username: minuscole, cifre e trattini, 1-39 caratteri, inizia e finisce
// alfanumerico. Vuoto → "user".
func NormalizeUsername(s string) string {
	s = badUsernameChars.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 39 {
		s = strings.Trim(s[:39], "-")
	}
	if s == "" {
		return "user"
	}
	return s
}

// usernameWithSuffix: n=1 è il nome intatto, poi "-2", "-3"…, sempre entro 39
// caratteri.
func usernameWithSuffix(base string, n int) string {
	if n <= 1 {
		return base
	}
	suf := "-" + strconv.Itoa(n)
	if len(base)+len(suf) > 39 {
		base = strings.Trim(base[:39-len(suf)], "-")
	}
	return base + suf
}
