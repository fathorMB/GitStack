// Package trust verifica che l'identità di una richiesta arrivi dal gateway.
//
// Il gateway autentica il client (sessione o token, via identity) e inoltra a
// identity e core chi è, in header X-Gitstack-* firmati con il segreto di
// servizio. Identity accetta questa identità solo per le richieste senza
// cookie di sessione (token personali); firma mancante, non valida o
// scaduta = nessuna identità. Lo schema è identico a
// services/gateway/internal/trust e services/core/internal/trust:
//
//	X-Gitstack-User-Id    id dell'utente (UUID)
//	X-Gitstack-Username   username
//	X-Gitstack-Scopes     scope del token separati da virgola; vuoto per le
//	                      sessioni web
//	X-Gitstack-Token-Id   id del token usato (vuoto per le sessioni web)
//	X-Gitstack-Token-Name nome del token usato (vuoto per le sessioni web)
//	X-Gitstack-Timestamp  secondi Unix del momento della firma
//	X-Gitstack-Signature  hex(HMAC-SHA256(segreto di servizio,
//	                      "gitstack-identity-v1\n" + timestamp + "\n" +
//	                      userId + "\n" + username + "\n" + scopes + "\n" +
//	                      tokenId + "\n" + tokenName))
//
// Il segreto è quello del Secret `<release>-identity-service`, chiave
// `secret` (variabile GITSTACK_IDENTITY_SERVICE_SECRET); non viene mai
// loggato. Una firma vale MaxSkew prima e dopo il timestamp.
package trust

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Nomi degli header d'identità.
const (
	HeaderUserID    = "X-Gitstack-User-Id"
	HeaderUsername  = "X-Gitstack-Username"
	HeaderScopes    = "X-Gitstack-Scopes"
	HeaderTokenID   = "X-Gitstack-Token-Id"
	HeaderTokenName = "X-Gitstack-Token-Name"
	HeaderTimestamp = "X-Gitstack-Timestamp"
	HeaderSignature = "X-Gitstack-Signature"
)

// MaxSkew è lo scarto massimo fra il timestamp firmato e l'orologio di identity.
const MaxSkew = 60 * time.Second

const signaturePrefix = "gitstack-identity-v1\n"

// Identity è chi ha fatto la richiesta, come dichiarato dal gateway.
type Identity struct {
	UserID   string
	Username string
	Scopes   []string
	// TokenID e TokenName sono il token con cui il client si è autenticato;
	// vuoti per le sessioni web e per le chiamate interne.
	TokenID   string
	TokenName string
}

// Sign scrive gli header d'identità firmati (è ciò che fa il gateway; qui
// serve ai test e a chi vuole chiamare identity in un test d'integrazione).
func Sign(h http.Header, secret string, id Identity, now time.Time) {
	scopes := strings.Join(id.Scopes, ",")
	ts := strconv.FormatInt(now.Unix(), 10)
	h.Set(HeaderUserID, id.UserID)
	h.Set(HeaderUsername, id.Username)
	h.Set(HeaderScopes, scopes)
	h.Set(HeaderTokenID, id.TokenID)
	h.Set(HeaderTokenName, id.TokenName)
	h.Set(HeaderTimestamp, ts)
	h.Set(HeaderSignature, mac(SigningKey(secret), ts, id.UserID, id.Username, scopes, id.TokenID, id.TokenName))
}

// Chiamanti noti: ognuno ha il suo segreto di servizio (F9, ASVS V2.10.1).
// Chi verifica sa da quale chiave è stata firmata una richiesta e quindi chi
// la manda; la chiave di un servizio non firma per un altro.
const (
	CallerGateway = "gateway"
	CallerCore    = "core"
	CallerGit     = "git"
)

// ParseKeys legge un segreto di servizio come elenco "nuova,vecchia": le
// chiavi sono separate da virgola e gli spazi attorno sono ignorati. La
// prima è quella con cui si firma, tutte sono accettate in verifica: è la
// doppia chiave valida durante una rotazione (docs/operations.md).
func ParseKeys(s string) []string {
	var keys []string
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// SigningKey è la chiave con cui si firma: la prima dell'elenco.
func SigningKey(secret string) string {
	if k := ParseKeys(secret); len(k) > 0 {
		return k[0]
	}
	return ""
}

// Keyring associa a ogni chiamante le chiavi che si accettano da lui.
type Keyring map[string][]string

// NewKeyring costruisce un Keyring da elenchi "nuova,vecchia" per chiamante;
// i chiamanti senza chiavi non entrano.
func NewKeyring(secrets map[string]string) Keyring {
	k := Keyring{}
	for caller, s := range secrets {
		if keys := ParseKeys(s); len(keys) > 0 {
			k[caller] = keys
		}
	}
	return k
}

// Only ritorna il Keyring ristretto ai chiamanti indicati.
func (k Keyring) Only(callers ...string) Keyring {
	out := Keyring{}
	for _, c := range callers {
		if keys, ok := k[c]; ok {
			out[c] = keys
		}
	}
	return out
}

// MatchBearer cerca fra le chiavi del Keyring quella uguale a got (in tempo
// costante sugli SHA-256, per non rivelare la lunghezza) e ritorna il
// chiamante a cui appartiene.
func (k Keyring) MatchBearer(got string) (string, bool) {
	if got == "" {
		return "", false
	}
	a := sha256.Sum256([]byte(got))
	for _, caller := range k.callers() {
		keys := k[caller]
		for _, key := range keys {
			b := sha256.Sum256([]byte(key))
			if subtle.ConstantTimeCompare(a[:], b[:]) == 1 {
				return caller, true
			}
		}
	}
	return "", false
}

// callers elenca i chiamanti in ordine fisso: con chiavi uguali fra chiamanti
// (configurazione errata o segreto condiviso storico) vince sempre lo stesso.
func (k Keyring) callers() []string {
	names := make([]string, 0, len(k))
	for c := range k {
		names = append(names, c)
	}
	sort.Strings(names)
	return names
}

// Verify controlla gli header d'identità di una richiesta con il segreto
// dato (un elenco "nuova,vecchia" è accettato: vale una qualunque delle
// chiavi). ok è false se manca uno degli header, se uno è ripetuto, se il
// timestamp è fuori finestra o se la firma non torna.
func Verify(h http.Header, secret string, now time.Time) (Identity, bool) {
	keys := ParseKeys(secret)
	if len(keys) == 0 {
		return Identity{}, false
	}
	id, _, ok := VerifyRing(h, Keyring{"": keys}, now)
	return id, ok
}

// VerifyRing è Verify con un chiamante per chiave: oltre all'identità
// ritorna il chiamante la cui chiave ha firmato la richiesta.
func VerifyRing(h http.Header, ring Keyring, now time.Time) (Identity, string, bool) {
	if len(ring) == 0 {
		return Identity{}, "", false
	}
	get := func(name string) (string, bool) {
		v := h.Values(name)
		if len(v) != 1 {
			return "", false
		}
		return v[0], true
	}
	userID, ok1 := get(HeaderUserID)
	username, ok2 := get(HeaderUsername)
	scopes, ok3 := get(HeaderScopes)
	tokenID, ok6 := get(HeaderTokenID)
	tokenName, ok7 := get(HeaderTokenName)
	ts, ok4 := get(HeaderTimestamp)
	sig, ok5 := get(HeaderSignature)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 || userID == "" || username == "" {
		return Identity{}, "", false
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return Identity{}, "", false
	}
	skew := now.Sub(time.Unix(secs, 0))
	if skew > MaxSkew || skew < -MaxSkew {
		return Identity{}, "", false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return Identity{}, "", false
	}
	caller, matched := "", false
	for _, c := range ring.callers() {
		keys := ring[c]
		for _, key := range keys {
			want, _ := hex.DecodeString(mac(key, ts, userID, username, scopes, tokenID, tokenName))
			if hmac.Equal(got, want) {
				caller, matched = c, true
				break
			}
		}
		if matched {
			break
		}
	}
	if !matched {
		return Identity{}, "", false
	}
	id := Identity{UserID: userID, Username: username, TokenID: tokenID, TokenName: tokenName}
	if scopes != "" {
		id.Scopes = strings.Split(scopes, ",")
	}
	return id, caller, true
}

func mac(secret, ts, userID, username, scopes, tokenID, tokenName string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(signaturePrefix + ts + "\n" + userID + "\n" + username + "\n" + scopes + "\n" + tokenID + "\n" + tokenName))
	return hex.EncodeToString(m.Sum(nil))
}

type ctxKey struct{}

type callerKey struct{}

// FromContext ritorna l'identità verificata, se la richiesta è passata da
// Require.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// CallerFromContext ritorna il chiamante la cui chiave ha firmato
// l'identità (vuoto se la richiesta non è passata da RequireRing).
func CallerFromContext(ctx context.Context) string {
	c, _ := ctx.Value(callerKey{}).(string)
	return c
}

// Require è il middleware che accetta la richiesta solo con un'identità
// firmata con il segreto dato; altrimenti 401 `unauthenticated` (formato
// Error del contratto). Le rotte per cui exempt ritorna true (probe e
// /health, che nel contratto è `security: []`) passano senza identità. now
// è iniettabile per i test (nil = time.Now).
func Require(secret string, exempt func(*http.Request) bool, now func() time.Time) func(http.Handler) http.Handler {
	return RequireRing(Keyring{"": ParseKeys(secret)}, nil, exempt, now)
}

// RequireRing è Require con un segreto per chiamante. allow, se non è nil,
// decide se quel chiamante può fare quella richiesta: un'identità firmata
// valida di un chiamante non autorizzato per la rotta riceve 403
// `forbidden`.
func RequireRing(ring Keyring, allow func(caller string, r *http.Request) bool, exempt func(*http.Request) bool, now func() time.Time) func(http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			id, caller, ok := VerifyRing(r.Header, ring, now())
			if !ok {
				writeErr(w, http.StatusUnauthorized, "unauthenticated", "Identità mancante o non proveniente dal gateway.")
				return
			}
			if allow != nil && !allow(caller, r) {
				writeErr(w, http.StatusForbidden, "forbidden", "Il servizio chiamante non può fare questa operazione.")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, id)
			ctx = context.WithValue(ctx, callerKey{}, caller)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
