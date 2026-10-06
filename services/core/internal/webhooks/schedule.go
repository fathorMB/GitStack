package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"
	"unicode/utf8"
)

// Costanti di C7 e C8 (docs/webhooks.md).
const (
	// MaxAttempts: tentativi per consegna, in circa 24 ore.
	MaxAttempts = 8
	// DisableAfter: dopo tanti giorni di fallimenti consecutivi il webhook si
	// disattiva.
	DisableAfter = 3 * 24 * time.Hour
	// LogRetention: il log delle consegne si conserva 30 giorni.
	LogRetention = 30 * 24 * time.Hour
	// RequestTimeout: una risposta oltre i 10 secondi è un timeout.
	RequestTimeout = 10 * time.Second
	// MaxBody: la risposta nel log è troncata a 4 KB.
	MaxBody = 4096
	// PayloadVersion è la versione del formato (docs/webhooks.md).
	PayloadVersion = 1
	// UserAgent delle consegne.
	UserAgent = "GitStack-Webhook/1"
)

// retryDelays è l'attesa dopo il tentativo n (indice n-1) fallito: subito, poi
// 1 min, 5 min, 30 min, 2 h, 4 h, 8 h e 9 h (23 h 36 min dopo il primo).
var retryDelays = [MaxAttempts - 1]time.Duration{
	time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 9 * time.Hour,
}

// RetryDelay è l'attesa prima del tentativo successivo a quello numero n
// (1-based); false se n è l'ultimo.
func RetryDelay(n int) (time.Duration, bool) {
	if n < 1 || n >= MaxAttempts {
		return 0, false
	}
	return retryDelays[n-1], true
}

// Sign è il valore di X-GitStack-Signature: sha256= e l'HMAC-SHA256 del corpo,
// in esadecimale minuscolo, con il segreto come chiave.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// clipUTF8 rende s adatto a una colonna TEXT: niente NUL né byte non UTF-8, al
// massimo max byte (tagliati su un confine di carattere).
func clipUTF8(s string, max int) string {
	if !utf8.ValidString(s) {
		s = toValid(s)
	}
	b := make([]rune, 0, len(s))
	n := 0
	for _, r := range s {
		if r == 0 {
			continue
		}
		l := utf8.RuneLen(r)
		if n+l > max {
			break
		}
		n += l
		b = append(b, r)
	}
	return string(b)
}

func toValid(s string) string {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, '�')
		} else {
			out = append(out, r)
		}
		i += size
	}
	return string(out)
}
