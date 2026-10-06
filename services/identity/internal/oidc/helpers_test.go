package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
)

const (
	testClientID     = "gitstack-test"
	testClientSecret = "s3cr3t-value-for-tests"
	testKeyID        = "k1"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// ---- IdP finto (discovery, JWKS, token endpoint con controllo PKCE) ----

type authCode struct {
	nonce, challenge, redirect string
}

type fakeIdP struct {
	t     *testing.T
	srv   *httptest.Server
	key   *rsa.PrivateKey
	other *rsa.PrivateKey // chiave "sbagliata" per le firme false
	kid   string

	mu    sync.Mutex
	codes map[string]authCode

	// Manopole dei test.
	claims    func(c map[string]any, a authCode) // modifica i claim del token ID
	signWith  *rsa.PrivateKey                    // firma con un'altra chiave
	noIDToken bool
	tokenHits int
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	o, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{t: t, key: k, other: o, kid: "kid-1", codes: map[string]authCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": p.kid,
			"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIdP) hits() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokenHits
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// authorize simula l'utente che ha fatto login presso l'IdP: legge
// l'authorization URL come farebbe l'IdP e ritorna il code.
func (p *fakeIdP) authorize(authURL string) (code, state string) {
	p.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		p.t.Fatal(err)
	}
	q := u.Query()
	if !strings.HasPrefix(authURL, p.srv.URL+"/authorize?") {
		p.t.Fatalf("authorization URL inatteso: %s", authURL)
	}
	if q.Get("response_type") != "code" || q.Get("client_id") != testClientID {
		p.t.Fatalf("parametri OIDC inattesi: %v", q)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		p.t.Fatalf("PKCE S256 assente: %v", q)
	}
	if q.Get("state") == "" || q.Get("nonce") == "" {
		p.t.Fatalf("state o nonce assenti: %v", q)
	}
	code = "code-" + uuid.NewString()
	p.mu.Lock()
	p.codes[code] = authCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	p.mu.Unlock()
	return code, q.Get("state")
}

func (p *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.tokenHits++
	p.mu.Unlock()
	_ = r.ParseForm()
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	} else {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	}
	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": msg})
	}
	if id != testClientID || secret != testClientSecret {
		fail("client non valido")
		return
	}
	p.mu.Lock()
	a, found := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code")) // monouso
	p.mu.Unlock()
	if !found {
		fail("code sconosciuto o già usato")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if b64(sum[:]) != a.challenge {
		fail("PKCE: code_verifier non corrisponde")
		return
	}
	if r.PostForm.Get("redirect_uri") != a.redirect {
		fail("redirect_uri diversa")
		return
	}
	out := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 60}
	if !p.noIDToken {
		now := time.Now()
		claims := map[string]any{
			"iss": p.srv.URL, "aud": testClientID, "sub": "sub-1",
			"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": a.nonce,
			"email": "alice@example.com", "email_verified": true,
			"preferred_username": "alice", "name": "Alice Example",
		}
		if p.claims != nil {
			p.claims(claims, a)
		}
		key := p.key
		if p.signWith != nil {
			key = p.signWith
		}
		out["id_token"] = p.sign(claims, key)
	}
	writeJSONTest(w, out)
}

func (p *fakeIdP) sign(claims map[string]any, key *rsa.PrivateKey) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": p.kid})
	body, _ := json.Marshal(claims)
	in := b64(hdr) + "." + b64(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return in + "." + b64(sig)
}

// ---- Repository in memoria ----

type memRepo struct {
	mu         sync.Mutex
	rows       map[string]ProviderRow
	enabled    map[string]bool
	users      map[uuid.UUID]users.User
	identities map[string]uuid.UUID // providerID|subject -> user
	sessions   int
	deleted    []string
}

func newMemRepo() *memRepo {
	return &memRepo{
		rows: map[string]ProviderRow{}, enabled: map[string]bool{},
		users: map[uuid.UUID]users.User{}, identities: map[string]uuid.UUID{},
	}
}

func (m *memRepo) SyncProviders(_ context.Context, rows []SyncRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range m.enabled {
		m.enabled[s] = false
	}
	for _, r := range rows {
		id := m.rows[r.Slug].ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		m.rows[r.Slug] = ProviderRow{ID: id, Slug: r.Slug, Issuer: r.Issuer, ClientID: r.ClientID,
			SecretEnc: r.SecretEnc, EncKeyID: r.EncKeyID, Scopes: r.Scopes}
		m.enabled[r.Slug] = true
	}
	return nil
}

func (m *memRepo) Provider(_ context.Context, slug string) (ProviderRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[slug]; ok && m.enabled[slug] {
		return r, nil
	}
	return ProviderRow{}, ErrProviderNotFound
}

func (m *memRepo) IdentityUser(_ context.Context, pid uuid.UUID, subject string) (users.User, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uid, ok := m.identities[pid.String()+"|"+subject]
	if !ok {
		return users.User{}, false, nil
	}
	return m.users[uid], true, nil
}

func (m *memRepo) TouchIdentity(context.Context, uuid.UUID, string, string) error { return nil }

func (m *memRepo) UserByEmail(_ context.Context, email string) (users.User, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email != nil && strings.EqualFold(*u.Email, email) {
			return u, true, nil
		}
	}
	return users.User{}, false, nil
}

func (m *memRepo) LinkIdentity(_ context.Context, uid, pid uuid.UUID, subject, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := pid.String() + "|" + subject
	if _, ok := m.identities[k]; ok {
		return ErrIdentityExists
	}
	m.identities[k] = uid
	return nil
}

func (m *memRepo) CreateUser(_ context.Context, in users.CreateInput) (users.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Username == in.Username {
			return users.User{}, &users.AlreadyExistsError{Field: "username"}
		}
		if in.Email != "" && u.Email != nil && strings.EqualFold(*u.Email, in.Email) {
			return users.User{}, &users.AlreadyExistsError{Field: "email"}
		}
	}
	u := users.User{ID: uuid.New(), Username: in.Username, DisplayName: in.DisplayName, Kind: in.Kind, IsActive: true}
	if in.Email != "" {
		e := in.Email
		u.Email = &e
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *memRepo) DeleteUser(_ context.Context, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, u := range m.users {
		if u.Username == username {
			delete(m.users, id)
			m.deleted = append(m.deleted, username)
		}
	}
	return nil
}

func (m *memRepo) CreateSession(_ context.Context, uid uuid.UUID, _, _ string) (string, sessions.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions++
	now := time.Now()
	return fmt.Sprintf("session-%d", m.sessions), sessions.Session{
		ID: uuid.New(), UserID: uid, AuthMethod: "oidc", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, nil
}

func (m *memRepo) addUser(username, email string, active bool) users.User {
	u := users.User{ID: uuid.New(), Username: username, Kind: users.KindHuman, IsActive: active}
	if email != "" {
		u.Email = &email
	}
	m.mu.Lock()
	m.users[u.ID] = u
	m.mu.Unlock()
	return u
}

// ---- assemblaggio ----

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	t     *testing.T
	idp   *fakeIdP
	repo  *memRepo
	svc   *Service
	clock *clock
}

func newRig(t *testing.T, mod func(p *Provider)) *rig {
	t.Helper()
	idp := newIdP(t)
	p := Provider{
		Slug: "kc", DisplayName: "Keycloak", Issuer: idp.srv.URL, ClientID: testClientID, ClientSecret: testClientSecret,
		Scopes: []string{"openid", "profile", "email"},
		Claims: Claims{Email: DefaultClaimEmail, EmailVerified: DefaultClaimEmailVerified,
			Username: DefaultClaimUsername, DisplayName: DefaultClaimDisplayName},
	}
	if mod != nil {
		mod(&p)
	}
	repo := newMemRepo()
	clk := &clock{t: time.Now()}
	svc, err := New(repo, Config{Providers: []Provider{p}, Key: testKey, KeyID: testKeyID, PublicURL: "https://git.test"},
		slog.New(slog.NewTextHandler(io.Discard, nil)), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	svc.HTTPClient = idp.srv.Client()
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, idp: idp, repo: repo, svc: svc, clock: clk}
}

// begin esegue start e l'autorizzazione presso l'IdP: ritorna l'input della
// callback con cookie e state giusti.
func (r *rig) begin(redirectTo string) FinishInput {
	r.t.Helper()
	res, err := r.svc.Start(context.Background(), "kc", redirectTo, true)
	if err != nil {
		r.t.Fatalf("Start: %v", err)
	}
	code, state := r.idp.authorize(res.URL)
	return FinishInput{Slug: "kc", Code: code, State: state, StateCookie: res.Cookie.Value, UserAgent: "test", IP: "203.0.113.9"}
}

func (r *rig) finish(in FinishInput) (FinishResult, error) {
	return r.svc.Finish(context.Background(), in)
}
