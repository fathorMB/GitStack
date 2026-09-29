// Package trust definisce come il gateway comunica ai servizi a valle chi ha
// fatto la richiesta, in modo che core (e gli altri) si fidino dell'identità
// solo se arriva davvero dal gateway.
//
// Schema (versione 1), identico in services/core/internal/trust:
//
//	X-Gitstack-User-Id    id dell'utente (UUID)
//	X-Gitstack-Username   username
//	X-Gitstack-Scopes     scope del token separati da virgola; vuoto per le
//	                      sessioni web (che non hanno scope)
//	X-Gitstack-Timestamp  secondi Unix del momento della firma
//	X-Gitstack-Signature  hex(HMAC-SHA256(segreto di servizio,
//	                      "gitstack-identity-v1\n" + timestamp + "\n" +
//	                      userId + "\n" + username + "\n" + scopes))
//
// Il segreto è quello di servizio (Secret `<release>-identity-service`,
// chiave `secret`): lo conoscono solo gateway, identity e core, mai il
// client. Chi lo ignora non può fabbricare una firma valida; il timestamp
// limita il riuso di una firma catturata (finestra di 60 s lato core). Il
// gateway cancella sempre ogni header X-Gitstack-* ricevuto dal client prima
// di scrivere i propri.
package trust

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	HeaderTimestamp = "X-Gitstack-Timestamp"
	HeaderSignature = "X-Gitstack-Signature"

	// Prefix è il prefisso riservato: ogni header che lo usa arrivato dal
	// client viene eliminato dal gateway.
	Prefix = "X-Gitstack-"
)

const signaturePrefix = "gitstack-identity-v1\n"

// Identity è chi ha fatto la richiesta.
type Identity struct {
	UserID   string
	Username string
	Scopes   []string
}

// Sign scrive gli header d'identità firmati su h.
func Sign(h http.Header, secret string, id Identity, now time.Time) {
	scopes := strings.Join(id.Scopes, ",")
	ts := strconv.FormatInt(now.Unix(), 10)
	h.Set(HeaderUserID, id.UserID)
	h.Set(HeaderUsername, id.Username)
	h.Set(HeaderScopes, scopes)
	h.Set(HeaderTimestamp, ts)
	h.Set(HeaderSignature, mac(secret, ts, id.UserID, id.Username, scopes))
}

// StripClientHeaders elimina da h ogni header con il prefisso riservato.
func StripClientHeaders(h http.Header) {
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), Prefix) {
			delete(h, k)
		}
	}
}

func mac(secret, ts, userID, username, scopes string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(signaturePrefix + ts + "\n" + userID + "\n" + username + "\n" + scopes))
	return hex.EncodeToString(m.Sum(nil))
}

type ctxKey struct{}

// WithIdentity mette l'identità autenticata nel contesto della richiesta:
// il proxy la firma verso i servizi a valle.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext ritorna l'identità autenticata, se c'è.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
