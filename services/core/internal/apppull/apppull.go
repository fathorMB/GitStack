// Package apppull emette e verifica la credenziale con cui il cluster scarica le immagini del
// registry del repo per una app (GIT-425).
//
// È un token a firma (HMAC-SHA256), non uno stato nel database: nasce da core per app, non è di
// nessun utente, vale solo per leggere (packages: read) il registry del repo dell'app, scade da
// solo (si ruota riemettendolo: il controller lo fa prima della metà della validità) e sparisce di
// fatto con l'app o con il repo, perché chi lo verifica rilegge l'app dal database. Ha il prefisso
// «gsj_» del token del job, così il servizio git lo manda al ci-gateway (registry-access) senza
// conoscere un tipo nuovo di credenziale.
package apppull

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Username è lo username Basic del pull secret (il registry guarda solo la password).
const Username = "gitstack-app"

// TTL è la validità di un token; il controller lo riemette quando ne resta meno di metà.
const TTL = 30 * 24 * time.Hour

// RefreshAfter: età oltre la quale il controller riemette il token nel Secret dell'app.
const RefreshAfter = TTL / 3

// prefix: «gsj_» del token del job + marcatore del tipo.
const prefix = "gsj_app."

// ErrInvalid: token non valido, scaduto, firmato da una chiave sconosciuta o non di questo tipo.
var ErrInvalid = errors.New("apppull: token non valido")

// Is dice se il token ha la forma di un token di pull di app (non lo verifica).
func Is(token string) bool { return strings.HasPrefix(token, prefix) }

func sign(key string, payload []byte) []byte {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("gitstack-app-pull-v1\x00"))
	m.Write(payload)
	return m.Sum(nil)
}

// Mint emette il token dell'app, firmato con la prima chiave dell'elenco (formato di
// GITSTACK_SERVICE_SECRET_*: «nuova,vecchia»).
func Mint(keys []string, appID uuid.UUID, now time.Time) (string, error) {
	if len(keys) == 0 || keys[0] == "" {
		return "", errors.New("apppull: nessuna chiave di firma")
	}
	payload := make([]byte, 24)
	copy(payload, appID[:])
	binary.BigEndian.PutUint64(payload[16:], uint64(now.Add(TTL).Unix()))
	raw := append(payload, sign(keys[0], payload)...)
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Verify ritorna l'app del token se la firma torna con una delle chiavi e non è scaduto.
func Verify(keys []string, token string, now time.Time) (uuid.UUID, error) {
	if !Is(token) || len(token) > 128 {
		return uuid.Nil, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(prefix):])
	if err != nil || len(raw) != 24+sha256.Size {
		return uuid.Nil, ErrInvalid
	}
	payload, mac := raw[:24], raw[24:]
	ok := false
	for _, k := range keys {
		if k != "" && hmac.Equal(mac, sign(k, payload)) {
			ok = true
		}
	}
	if !ok || now.Unix() >= int64(binary.BigEndian.Uint64(payload[16:])) {
		return uuid.Nil, ErrInvalid
	}
	id, err := uuid.FromBytes(payload[:16])
	if err != nil {
		return uuid.Nil, ErrInvalid
	}
	return id, nil
}
