// Package webhooks è la consegna dei webhook di core (M-06/G, GIT-135; regole
// C6, C7 e C8 in .prisma/knowledge/topics/collegamenti-notifiche-webhook.md,
// formato in docs/webhooks.md).
//
// Come funziona, in tre pezzi che non tengono stato in memoria:
//
//  1. Creazione delle consegne. Gli eventi di dominio issue.*, issue_comment.*
//     e repository.* sono già nell'outbox transazionale (0009): il motore li
//     legge da lì e, nella STESSA transazione in cui li segna elaborati
//     (event_outbox.webhooked_at, 0012), scrive una riga pending in
//     core.webhook_deliveries per ogni webhook interessato. I push arrivano
//     da NATS (git.push, consumer in internal/gitpush) e diventano righe
//     pending allo stesso modo. L'indice unico (webhook_id, source_event_id)
//     rende idempotente anche una riconsegna di NATS.
//  2. Consegna. Un ciclo prende le righe pending con next_attempt_at scaduto,
//     le «affitta» spostando next_attempt_at (nessun doppio invio fra repliche,
//     e un crash a metà si riprende da solo alla scadenza dell'affitto), fa
//     la POST solo tramite pkg/egress (C8) e scrive l'esito.
//  3. Conservazione: il log si tiene 30 giorni (C7).
//
// Un riavvio di core non perde niente: la coda è la tabella.
package webhooks

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrNoKey: il segreto dei webhook non si può cifrare né decifrare perché core
// non ha la chiave (GITSTACK_WEBHOOK_SECRET_KEY).
var ErrNoKey = errors.New("webhooks: chiave dei segreti non configurata")

// Keyring cifra e decifra i segreti dei webhook (AES-256-GCM). Cifra con la
// chiave primaria e decifra con quella indicata da secret_key_id, così una
// rotazione non rende illeggibili le righe vecchie. Un *Keyring nil non
// cifra niente: ErrNoKey.
type Keyring struct {
	primary string
	keys    map[string][]byte
}

// NewKeyring costruisce il portachiavi. primaryKey è lunga 32 byte, in
// esadecimale (64 caratteri) o in base64; old è `id:chiave,id:chiave` con le
// chiavi precedenti. Con primaryKey vuota ritorna nil (nessun segreto).
func NewKeyring(primaryID, primaryKey, old string) (*Keyring, error) {
	if strings.TrimSpace(primaryKey) == "" {
		return nil, nil
	}
	if primaryID == "" {
		return nil, errors.New("webhooks: l'id della chiave dei segreti è vuoto")
	}
	k := &Keyring{primary: primaryID, keys: map[string][]byte{}}
	b, err := parseKey(primaryKey)
	if err != nil {
		return nil, fmt.Errorf("webhooks: chiave dei segreti non valida: %w", err)
	}
	k.keys[primaryID] = b
	for _, part := range strings.Split(old, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, raw, ok := strings.Cut(part, ":")
		if !ok || id == "" {
			return nil, fmt.Errorf("webhooks: chiave precedente %q: serve id:chiave", id)
		}
		if _, dup := k.keys[id]; dup {
			return nil, fmt.Errorf("webhooks: id di chiave ripetuto: %q", id)
		}
		b, err := parseKey(raw)
		if err != nil {
			return nil, fmt.Errorf("webhooks: chiave precedente %q non valida: %w", id, err)
		}
		k.keys[id] = b
	}
	return k, nil
}

func parseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("deve essere di 32 byte, in esadecimale (64 caratteri) o in base64")
}

// Enabled dice se core può cifrare i segreti.
func (k *Keyring) Enabled() bool { return k != nil }

func (k *Keyring) gcm(keyID string) (cipher.AEAD, error) {
	if k == nil {
		return nil, ErrNoKey
	}
	key, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("webhooks: chiave %q sconosciuta (rotazione senza la chiave precedente?)", keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal cifra il segreto del webhook con la chiave primaria. L'id del webhook è
// dato autenticato: un segreto copiato in un'altra riga non si decifra.
func (k *Keyring) Seal(webhookID uuid.UUID, secret string) (ciphertext, nonce []byte, keyID string, err error) {
	g, err := k.gcm(k.primaryID())
	if err != nil {
		return nil, nil, "", err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", err
	}
	return g.Seal(nil, nonce, []byte(secret), webhookID[:]), nonce, k.primary, nil
}

func (k *Keyring) primaryID() string {
	if k == nil {
		return ""
	}
	return k.primary
}

// Open decifra il segreto di un webhook.
func (k *Keyring) Open(webhookID uuid.UUID, ciphertext, nonce []byte, keyID string) (string, error) {
	g, err := k.gcm(keyID)
	if err != nil {
		return "", err
	}
	plain, err := g.Open(nil, nonce, ciphertext, webhookID[:])
	if err != nil {
		return "", fmt.Errorf("webhooks: segreto non decifrabile: %w", err)
	}
	return string(plain), nil
}
