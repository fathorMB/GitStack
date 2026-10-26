// Package appauth è il login GitStack davanti alle app interne (v3-a, GIT-347, H6, ADR 0002).
//
// Traefik, per ogni richiesta a un'app con accesso `login`, chiede al gateway (forwardAuth,
// GET/POST /_appauth/verify) se può passare. Il gateway:
//
//   - riconosce chi è: token con `read:resource` (agenti) o, per i browser, la sessione GitStack
//     (se il cookie arriva all'host dell'app) oppure il cookie d'app che il gateway stesso rilascia
//     dopo il passaggio dal login GitStack (/_appauth/login → /_gitstack/callback);
//   - decide con UNA funzione (AccessPolicy): di default `read` sul repo dell'app (repo interno →
//     tutti gli utenti, privato → chi ha un ruolo). È l'unico punto in cui estendere la regola;
//   - risponde 2xx con le intestazioni X-GitStack-* firmate (Ed25519, chiave pubblica su
//     /.well-known/gitstack-app-keys) e con Cookie/Authorization ripuliti dalle credenziali
//     GitStack: Traefik li sostituisce a quelli del client (authResponseHeaders), quindi né il
//     cookie di sessione né il token arrivano all'app;
//   - nega con 401/403 (o un redirect al login per le navigazioni del browser).
//
// Le intestazioni X-GitStack-* mandate dal client sono comunque scartate prima, dal Middleware
// `gitstack-strip` creato da core e da authResponseHeaders (Traefik cancella la voce prima di
// copiare quella del gateway).
package appauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Nomi delle intestazioni che l'app riceve.
const (
	HeaderUser      = "X-GitStack-User"
	HeaderEmail     = "X-GitStack-Email"
	HeaderTeams     = "X-GitStack-Teams"
	HeaderAuthTime  = "X-GitStack-Auth-Time"
	HeaderKeyID     = "X-GitStack-Key-Id"
	HeaderSignature = "X-GitStack-Signature"
)

const messagePrefix = "gitstack-app-identity-v1\n"

// Signed è ciò che la firma copre: la richiesta (metodo, host, percorso) e chi la fa. Il percorso è
// quello della richiesta senza query, nella forma in cui il client lo ha mandato.
type Signed struct {
	Method   string
	Host     string
	Path     string
	AuthTime string // secondi Unix, come stringa
	User     string
	Email    string
	Teams    string // «org/team» separati da virgola
}

// Message è il testo canonico firmato:
//
//	"gitstack-app-identity-v1\n" + method + "\n" + host + "\n" + path + "\n" + authTime + "\n" +
//	user + "\n" + email + "\n" + teams
func (s Signed) Message() []byte {
	return []byte(messagePrefix + strings.ToUpper(s.Method) + "\n" + strings.ToLower(s.Host) + "\n" + s.Path + "\n" +
		s.AuthTime + "\n" + s.User + "\n" + s.Email + "\n" + s.Teams)
}

// PublicKey è una chiave pubblica di verifica, come la espone /.well-known/gitstack-app-keys.
type PublicKey struct {
	KeyID     string `json:"kid"`
	Algorithm string `json:"alg"`
	// PublicKey è la chiave Ed25519 di 32 byte in base64 standard.
	PublicKey string `json:"publicKey"`
}

type keyPair struct {
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// Signer firma le intestazioni con la prima chiave; le altre (rotazione) restano pubblicate per
// far verificare alle app le firme già emesse.
type Signer struct{ keys []keyPair }

// KeyID è l'identificativo di una chiave pubblica: i primi 8 byte del suo SHA-256, in esadecimale.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ParseSigningKeys legge un elenco «nuova,vecchia» di semi Ed25519 (32 byte in base64, per
// esempio `openssl rand -base64 32`). La prima chiave firma. Elenco vuoto: errore.
func ParseSigningKeys(list string) (*Signer, error) {
	var s Signer
	for _, raw := range strings.Split(list, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		seed, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("la chiave di firma deve essere un seme di 32 byte in base64")
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		s.keys = append(s.keys, keyPair{kid: KeyID(pub), priv: priv, pub: pub})
	}
	if len(s.keys) == 0 {
		return nil, errors.New("nessuna chiave di firma")
	}
	return &s, nil
}

// Sign firma il messaggio con la chiave corrente e ritorna key id e firma (base64url senza padding).
func (s *Signer) Sign(msg []byte) (kid, sig string) {
	k := s.keys[0]
	return k.kid, base64.RawURLEncoding.EncodeToString(ed25519.Sign(k.priv, msg))
}

// PublicKeys sono le chiavi di verifica, la corrente per prima.
func (s *Signer) PublicKeys() []PublicKey {
	out := make([]PublicKey, len(s.keys))
	for i, k := range s.keys {
		out[i] = PublicKey{KeyID: k.kid, Algorithm: "Ed25519", PublicKey: base64.StdEncoding.EncodeToString(k.pub)}
	}
	return out
}

// VerifySignature è l'implementazione di riferimento di ciò che deve fare un'app: ricalcola il
// messaggio dalla richiesta ricevuta e controlla la firma con la chiave pubblica di key id kid.
// Non controlla l'età di authTime: lo fa il chiamante (vedi docs/apps-login.md).
func VerifySignature(keys []PublicKey, kid, sig string, in Signed) error {
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return errors.New("firma non valida")
	}
	for _, k := range keys {
		if k.KeyID != kid {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("chiave %s non valida", kid)
		}
		if !ed25519.Verify(pub, in.Message(), raw) {
			return errors.New("firma non valida")
		}
		return nil
	}
	return fmt.Errorf("chiave %s sconosciuta", kid)
}
