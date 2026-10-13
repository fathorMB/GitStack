// Package totp implementa le primitive del secondo fattore (F11): codici
// TOTP di RFC 6238 (HMAC-SHA1, 6 cifre, passo di 30 secondi, come si aspettano
// le app authenticator) e codici di recupero monouso.
//
// Usa solo la libreria standard (crypto/hmac, crypto/sha1, encoding/base32).
// Nessuna funzione logga o ritorna segreti dentro un errore.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1: è l'algoritmo di RFC 6238 che le app authenticator si aspettano.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Period è la durata di un passo, in secondi.
	Period = 30
	// Digits è la lunghezza del codice.
	Digits = 6
	// SecretBytes è la lunghezza del segreto (160 bit, come raccomanda RFC 4226).
	SecretBytes = 20
	// Skew: si accettano il passo corrente e uno prima e uno dopo (±1).
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret genera un segreto casuale.
func NewSecret() ([]byte, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generazione del segreto non riuscita: %w", err)
	}
	return b, nil
}

// EncodeSecret è il segreto in base32 senza padding (formato delle app).
func EncodeSecret(secret []byte) string { return b32.EncodeToString(secret) }

// Step è il numero del passo di 30 secondi che contiene t.
func Step(t time.Time) int64 { return t.Unix() / Period }

// Code calcola il codice HOTP del contatore dato (RFC 4226 §5.3).
func Code(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // il contatore è non negativo
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%0*d", Digits, bin%1_000_000)
}

// Match cerca fra i passi t-Skew..t+Skew quello che produce code, in tempo
// costante rispetto al valore del codice. Ritorna il passo trovato; il
// chiamante deve rifiutare un passo già usato (anti-replay).
func Match(secret []byte, code string, t time.Time) (int64, bool) {
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(t)
	var found int64
	ok := false
	for d := int64(-Skew); d <= Skew; d++ {
		step := cur + d
		if step < 0 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1 {
			found, ok = step, true
		}
	}
	return found, ok
}

// ProvisioningURI è l'URI otpauth:// da trasformare in QR code.
func ProvisioningURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// recoveryAlphabet: base32 senza i caratteri ambigui (0/O, 1/I/L).
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// RecoveryCodeCount è il numero di codici di recupero generati all'attivazione.
const RecoveryCodeCount = 10

// NewRecoveryCode genera un codice di recupero "XXXXX-XXXXX" (10 caratteri su
// 31 simboli: circa 49 bit).
func NewRecoveryCode() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generazione del codice di recupero non riuscita: %w", err)
	}
	var b strings.Builder
	for i, c := range raw {
		if i == 5 {
			b.WriteByte('-')
		}
		b.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
	}
	return b.String(), nil
}

// NormalizeRecoveryCode porta un codice digitato dall'utente alla forma di
// confronto: maiuscole, senza trattini né spazi.
func NormalizeRecoveryCode(s string) string {
	s = strings.ToUpper(s)
	s = strings.NewReplacer("-", "", " ", "").Replace(s)
	return s
}
