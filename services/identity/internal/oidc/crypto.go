package oidc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Le due cifrature usano la stessa chiave (GITSTACK_IDENTITY_OIDC_ENC_KEY) ma
// AAD diverse, così un blob non è valido nell'altro contesto.
var (
	aadSecret = []byte("gitstack-identity-oidc-client-secret")
	aadState  = []byte("gitstack-identity-oidc-state-cookie")
)

var errCrypto = errors.New("dato cifrato non valido")

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("la chiave di cifratura deve essere di 32 byte")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal ritorna nonce‖ciphertext (AES-256-GCM).
func seal(key, plain, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generazione del nonce non riuscita: %w", err)
	}
	return g.Seal(nonce, nonce, plain, aad), nil
}

func open(key, blob, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < g.NonceSize()+g.Overhead() {
		return nil, errCrypto
	}
	plain, err := g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], aad)
	if err != nil {
		return nil, errCrypto
	}
	return plain, nil
}

// EncryptSecret cifra il client secret per identity.oidc_providers
// (client_secret_enc = nonce‖ciphertext).
func EncryptSecret(key []byte, secret string) ([]byte, error) {
	return seal(key, []byte(secret), aadSecret)
}

// DecryptSecret è l'inverso di EncryptSecret.
func DecryptSecret(key, blob []byte) (string, error) {
	p, err := open(key, blob, aadSecret)
	return string(p), err
}

// flowState è lo stato di un login in corso, nel cookie cifrato.
type flowState struct {
	Slug       string `json:"s"`
	State      string `json:"st"`
	Nonce      string `json:"n"`
	Verifier   string `json:"v"`
	RedirectTo string `json:"r"`
	Expires    int64  `json:"e"` // unix secondi
}

func sealState(key []byte, st flowState) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	blob, err := seal(key, raw, aadState)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(blob), nil
}

func openState(key []byte, value string, now time.Time) (flowState, error) {
	blob, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return flowState{}, errCrypto
	}
	raw, err := open(key, blob, aadState)
	if err != nil {
		return flowState{}, err
	}
	var st flowState
	if err := json.Unmarshal(raw, &st); err != nil {
		return flowState{}, errCrypto
	}
	if now.Unix() >= st.Expires {
		return flowState{}, ErrStateExpired
	}
	return st, nil
}

// randomToken sono n byte casuali in base64url (state, nonce).
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generazione casuale non riuscita: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
