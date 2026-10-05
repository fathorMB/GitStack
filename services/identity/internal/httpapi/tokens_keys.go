package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/userkeys"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Option configura parti opzionali dell'handler.
type Option func(*server)

// WithTokens abilita i token personali (tag tokens) e la verifica dei token
// in /internal/verify.
func WithTokens(t *apitokens.Service) Option { return func(s *server) { s.tokens = t } }

// WithSSHKeys abilita le chiavi SSH (tag ssh-keys) e /internal/ssh-keys.
func WithSSHKeys(k *userkeys.Service) Option { return func(s *server) { s.keys = k } }

// ClientIPHeader è l'header con l'IP del client impostato dal gateway
// (stesso nome di gateway/internal/proxy.ClientIPHeader). Vale solo se la
// connessione viene da un proxy fidato.
const ClientIPHeader = "X-Gitstack-Client-Ip"

// WithTrustedProxies indica le reti da cui ci si fida di ClientIPHeader.
func WithTrustedProxies(nets []*net.IPNet) Option {
	return func(s *server) { s.trustedProxies = nets }
}

// WithServiceSecret imposta il segreto condiviso di servizio (schema
// serviceAuth) che protegge /internal/*. Vuoto = ogni chiamata interna è
// rifiutata con 401. La lettura dell'ambiente è di chi monta il server.
func WithServiceSecret(secret string) Option { return func(s *server) { s.serviceSecret = secret } }

// Durata della cache che il gateway può tenere: esito positivo 30 s, negativo
// 5 s (README del servizio).
const (
	cacheTTLActive   = 30
	cacheTTLInactive = 5
)

// serviceAuth protegge /internal/* con "Authorization: Bearer <segreto>",
// confrontato in tempo costante (sugli SHA-256, per non rivelare la
// lunghezza). Segreto non configurato: tutto rifiutato.
func (s *server) serviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.serviceSecret == "" || !ok || got == "" {
			unauthenticated(w)
			return
		}
		a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(s.serviceSecret))
		if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
			unauthenticated(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unavailable(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Funzione non disponibile.")
}

// page legge page/perPage con i default e i limiti del contratto.
func page(w http.ResponseWriter, p, pp *int) (int, int, bool) {
	page, perPage := 1, 20
	if p != nil {
		page = *p
	}
	if pp != nil {
		perPage = *pp
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return 0, 0, false
	}
	return page, perPage, true
}

// ---- token personali ----

func toScopes(in []string) []openapi.TokenScope {
	out := make([]openapi.TokenScope, 0, len(in))
	for _, s := range in {
		out = append(out, openapi.TokenScope(s))
	}
	return out
}

func toToken(t apitokens.Token) openapi.Token {
	id := openapi_types.UUID(t.ID)
	return openapi.Token{
		Id: &id, Name: t.Name, Scopes: toScopes(t.Scopes), Hint: t.Hint,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
	}
}

func (s *server) ListTokens(w http.ResponseWriter, r *http.Request, p openapi.ListTokensParams) {
	if s.tokens == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	pg, perPage, ok := page(w, p.Page, p.PerPage)
	if !ok {
		return
	}
	list, total, err := s.tokens.List(r.Context(), cur.User.ID, pg, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.Token, 0, len(list))
	for _, t := range list {
		items = append(items, toToken(t))
	}
	writeJSON(w, http.StatusOK, openapi.TokenList{Items: items, Page: pg, PerPage: perPage, Total: total})
}

func (s *server) CreateToken(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	var in openapi.CreateTokenInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		scopes = append(scopes, string(sc))
	}
	plain, t, err := s.tokens.Create(r.Context(), apitokens.CreateInput{
		UserID: cur.User.ID, Name: in.Name, Scopes: scopes, ExpiresAt: in.ExpiresAt,
	})
	var ve *apitokens.ValidationError
	var ne *apitokens.NameInUseError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
		return
	case errors.As(err, &ne):
		writeErrorDetails(w, http.StatusConflict, "already_exists", "name già in uso.", map[string]any{"field": "name"})
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	base := toToken(t)
	w.Header().Set("Location", "/v1/user/tokens/"+t.ID.String())
	writeJSON(w, http.StatusCreated, openapi.CreatedToken{
		Id: base.Id, Name: base.Name, Scopes: base.Scopes, Hint: base.Hint,
		CreatedAt: base.CreatedAt, ExpiresAt: base.ExpiresAt, LastUsedAt: base.LastUsedAt,
		Token: plain,
	})
}

func (s *server) RevokeToken(w http.ResponseWriter, r *http.Request, tokenId openapi.TokenIdParam) {
	if s.tokens == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	err := s.tokens.Revoke(r.Context(), cur.User.ID, tokenId)
	if errors.Is(err, apitokens.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Token non trovato.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- chiavi SSH ----

func toSSHKey(k userkeys.Key) openapi.SshKey {
	id := openapi_types.UUID(k.ID)
	return openapi.SshKey{
		Id: &id, Title: k.Title, KeyType: k.KeyType, Fingerprint: k.Fingerprint,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
	}
}

func (s *server) ListSshKeys(w http.ResponseWriter, r *http.Request, p openapi.ListSshKeysParams) {
	if s.keys == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	pg, perPage, ok := page(w, p.Page, p.PerPage)
	if !ok {
		return
	}
	list, total, err := s.keys.List(r.Context(), cur.User.ID, pg, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.SshKey, 0, len(list))
	for _, k := range list {
		items = append(items, toSSHKey(k))
	}
	writeJSON(w, http.StatusOK, openapi.SshKeyList{Items: items, Page: pg, PerPage: perPage, Total: total})
}

func (s *server) AddSshKey(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	var in openapi.AddSshKeyInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	k, err := s.keys.Add(r.Context(), cur.User.ID, in.Title, in.PublicKey)
	var ve *userkeys.ValidationError
	var fe *userkeys.FingerprintInUseError
	var te *userkeys.TitleInUseError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
		return
	case errors.As(err, &fe):
		writeError(w, http.StatusConflict, "ssh_key_in_use", "Questa chiave SSH è già registrata.")
		return
	case errors.As(err, &te):
		writeErrorDetails(w, http.StatusConflict, "already_exists", "title già in uso.", map[string]any{"field": "title"})
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/user/ssh-keys/"+k.ID.String())
	writeJSON(w, http.StatusCreated, toSSHKey(k))
}

func (s *server) GetSshKey(w http.ResponseWriter, r *http.Request, keyId openapi.SshKeyIdParam) {
	if s.keys == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	k, err := s.keys.Get(r.Context(), cur.User.ID, keyId)
	if errors.Is(err, userkeys.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Chiave SSH non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSSHKey(k))
}

func (s *server) DeleteSshKey(w http.ResponseWriter, r *http.Request, keyId openapi.SshKeyIdParam) {
	if s.keys == nil {
		unavailable(w)
		return
	}
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	err := s.keys.Delete(r.Context(), cur.User.ID, keyId)
	if errors.Is(err, userkeys.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Chiave SSH non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- interfaccia interna ----

// VerifyCredential risolve un cookie di sessione o un token gst_... Le
// credenziali sconosciute, scadute o revocate danno tutte active=false.
func (s *server) VerifyCredential(w http.ResponseWriter, r *http.Request) {
	var in openapi.VerifyCredentialInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if in.Credential == nil || *in.Credential == "" || len(*in.Credential) > 512 {
		writeError(w, http.StatusBadRequest, "bad_request", "credential: da 1 a 512 caratteri.")
		return
	}
	cred := *in.Credential
	isToken := strings.HasPrefix(cred, "gst_")
	if in.Kind != nil {
		isToken = *in.Kind == openapi.VerifyCredentialInputKind("token")
	}
	inactive := func() {
		ttl := cacheTTLInactive
		writeJSON(w, http.StatusOK, openapi.VerifyCredentialResult{Active: false, CacheTtlSeconds: &ttl})
	}
	ttl := cacheTTLActive
	if isToken {
		if s.tokens == nil {
			unavailable(w)
			return
		}
		p, err := s.tokens.Verify(r.Context(), cred)
		if errors.Is(err, apitokens.ErrInactive) {
			inactive()
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		id, uid := openapi_types.UUID(p.Token.ID), openapi_types.UUID(p.Token.UserID)
		sc := toScopes(p.Token.Scopes)
		writeJSON(w, http.StatusOK, openapi.VerifyCredentialResult{Active: true, CacheTtlSeconds: &ttl, Principal: &openapi.Principal{
			UserId: uid, Username: p.Username, Kind: openapi.PrincipalKind(p.Kind), IsAdmin: p.IsAdmin,
			AuthMethod: openapi.PrincipalAuthMethod("token"), Scopes: &sc, CredentialId: &id, TokenName: &p.Token.Name, ExpiresAt: p.Token.ExpiresAt,
		}})
		return
	}
	cur, err := s.auth.Session(r.Context(), cred)
	if errors.Is(err, auth.ErrUnauthenticated) {
		inactive()
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	id, uid := openapi_types.UUID(cur.Session.ID), openapi_types.UUID(cur.User.ID)
	exp := cur.Session.ExpiresAt
	writeJSON(w, http.StatusOK, openapi.VerifyCredentialResult{Active: true, CacheTtlSeconds: &ttl, Principal: &openapi.Principal{
		UserId: uid, Username: cur.User.Username, Kind: openapi.PrincipalKind(cur.User.Kind), IsAdmin: cur.User.IsAdmin,
		AuthMethod: openapi.PrincipalAuthMethod(cur.Session.AuthMethod), CredentialId: &id, ExpiresAt: &exp,
		// Password iniziale da cambiare: la sessione e' valida, ma il gateway
		// risponde 403 password_change_required a ogni rotta tranne le tre
		// eccezioni del contratto (x-password-change-exempt).
		MustChangePassword: &cur.MustChange,
	}})
}

// LookupSshKey risolve il fingerprint di una chiave nell'utente proprietario.
func (s *server) LookupSshKey(w http.ResponseWriter, r *http.Request, fingerprint string) {
	if s.keys == nil {
		unavailable(w)
		return
	}
	k, err := s.keys.Lookup(r.Context(), fingerprint)
	if errors.Is(err, userkeys.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Chiave SSH non trovata.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	u, err := s.users.GetByID(r.Context(), k.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.SshKeyLookup{Key: toSSHKey(k), User: toUser(u, true)})
}
