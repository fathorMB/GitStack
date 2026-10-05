// Package trust verifica che l'identità di una richiesta arrivi dal gateway o da core (copia di services/identity/internal/trust).
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
	"encoding/hex"
	"encoding/json"
	"net/http"
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

// MaxSkew è lo scarto massimo fra il timestamp firmato e l'orologio di git.
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
// serve ai test e a chi vuole chiamare git in un test d'integrazione).
func Sign(h http.Header, secret string, id Identity, now time.Time) {
	scopes := strings.Join(id.Scopes, ",")
	ts := strconv.FormatInt(now.Unix(), 10)
	h.Set(HeaderUserID, id.UserID)
	h.Set(HeaderUsername, id.Username)
	h.Set(HeaderScopes, scopes)
	h.Set(HeaderTokenID, id.TokenID)
	h.Set(HeaderTokenName, id.TokenName)
	h.Set(HeaderTimestamp, ts)
	h.Set(HeaderSignature, mac(secret, ts, id.UserID, id.Username, scopes, id.TokenID, id.TokenName))
}

// Verify controlla gli header d'identità di una richiesta. ok è false se
// manca uno degli header, se uno è ripetuto, se il timestamp è fuori
// finestra o se la firma non torna.
func Verify(h http.Header, secret string, now time.Time) (Identity, bool) {
	if secret == "" {
		return Identity{}, false
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
		return Identity{}, false
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return Identity{}, false
	}
	skew := now.Sub(time.Unix(secs, 0))
	if skew > MaxSkew || skew < -MaxSkew {
		return Identity{}, false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return Identity{}, false
	}
	want, _ := hex.DecodeString(mac(secret, ts, userID, username, scopes, tokenID, tokenName))
	if !hmac.Equal(got, want) {
		return Identity{}, false
	}
	id := Identity{UserID: userID, Username: username, TokenID: tokenID, TokenName: tokenName}
	if scopes != "" {
		id.Scopes = strings.Split(scopes, ",")
	}
	return id, true
}

func mac(secret, ts, userID, username, scopes, tokenID, tokenName string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(signaturePrefix + ts + "\n" + userID + "\n" + username + "\n" + scopes + "\n" + tokenID + "\n" + tokenName))
	return hex.EncodeToString(m.Sum(nil))
}

type ctxKey struct{}

// FromContext ritorna l'identità verificata, se la richiesta è passata da
// Require.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Require è il middleware che accetta la richiesta solo con un'identità
// firmata dal gateway; altrimenti 401 `unauthenticated` (formato Error del
// contratto). Le rotte per cui exempt ritorna true (probe e /health, che nel
// contratto è `security: []`) passano senza identità. now è iniettabile per
// i test (nil = time.Now).
func Require(secret string, exempt func(*http.Request) bool, now func() time.Time) func(http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			id, ok := Verify(r.Header, secret, now())
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
					"code":    "unauthenticated",
					"message": "Identità mancante o non proveniente dal gateway.",
				}})
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}
