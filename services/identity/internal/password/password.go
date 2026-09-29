// Package password calcola e verifica gli hash delle password locali con
// argon2id e applica la politica minima sulle password nuove.
//
// Formato salvato (credentials.secret_hash): stringa PHC
//
//	$argon2id$v=19$m=<KiB>,t=<passate>,p=<thread>$<salt base64>$<hash base64>
//
// (base64 standard senza padding). Salt e parametri stanno nella stringa,
// quindi un hash resta verificabile anche dopo un cambio dei parametri
// costanti qui sotto; NeedsRehash dice quando riscriverlo al login.
//
// Parametri (raccomandazione OWASP "argon2id m=19 MiB, t=2, p=1"):
//   - memoria 19456 KiB (19 MiB)
//   - iterazioni 2
//   - parallelismo 1
//   - salt 16 byte da crypto/rand, chiave derivata 32 byte
//
// La password e l'hash non compaiono mai in messaggi d'errore né in log.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Parametri correnti per gli hash nuovi.
const (
	Memory      uint32 = 19 * 1024 // KiB
	Iterations  uint32 = 2
	Parallelism uint8  = 1
	SaltLen            = 16
	KeyLen             = 32

	// MinLength e MaxLength sono i limiti di lunghezza (in caratteri) della
	// password, gli stessi del contratto (CreateUserInput/ChangePasswordInput).
	MinLength = 12
	MaxLength = 1024
)

var (
	// ErrInvalidHash: la stringa salvata non è un PHC argon2id valido.
	ErrInvalidHash = errors.New("hash della password non valido")
	// ErrTooShort, ErrTooLong, ErrBlank, ErrSameAsUsername sono i motivi di
	// rifiuto della politica.
	ErrTooShort       = errors.New("la password deve avere almeno 12 caratteri")
	ErrTooLong        = errors.New("la password non può superare 1024 caratteri")
	ErrBlank          = errors.New("la password non può essere fatta solo di spazi")
	ErrSameAsUsername = errors.New("la password non può coincidere con lo username")
)

// ValidatePolicy applica la politica minima: da 12 a 1024 caratteri, non
// composta solo da spazi, diversa dallo username (e dall'email, se data).
func ValidatePolicy(pw, username, email string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	case strings.TrimSpace(pw) == "":
		return ErrBlank
	}
	if pw == username || (email != "" && strings.EqualFold(pw, email)) {
		return ErrSameAsUsername
	}
	return nil
}

// Hash produce l'hash PHC argon2id di pw con i parametri correnti.
func Hash(pw string) (string, error) {
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generazione del salt non riuscita: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, Iterations, Memory, Parallelism, KeyLen)
	return encode(Memory, Iterations, Parallelism, salt, key), nil
}

func encode(m, t uint32, p uint8, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

type parsed struct {
	m, t uint32
	p    uint8
	salt []byte
	key  []byte
}

func parse(phc string) (parsed, error) {
	var out parsed
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return out, ErrInvalidHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return out, ErrInvalidHash
	}
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return out, ErrInvalidHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return out, ErrInvalidHash
		}
		switch k {
		case "m":
			out.m = uint32(n)
		case "t":
			out.t = uint32(n)
		case "p":
			if n > 255 {
				return out, ErrInvalidHash
			}
			out.p = uint8(n)
		default:
			return out, ErrInvalidHash
		}
	}
	// Limiti di sicurezza: un valore salvato manomesso non deve poter
	// chiedere gigabyte di memoria.
	if out.m == 0 || out.t == 0 || out.p == 0 || out.m > 1<<20 || out.t > 64 {
		return out, ErrInvalidHash
	}
	var err error
	if out.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(out.salt) == 0 {
		return out, ErrInvalidHash
	}
	if out.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(out.key) == 0 {
		return out, ErrInvalidHash
	}
	return out, nil
}

// Verify confronta pw con l'hash PHC a tempo costante. Restituisce true se
// combacia; errore solo se l'hash salvato è malformato.
func Verify(pw, phc string) (bool, error) {
	h, err := parse(phc)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), h.salt, h.t, h.m, h.p, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}

// NeedsRehash dice se l'hash è stato calcolato con parametri diversi da
// quelli correnti (da riscrivere dopo un login riuscito).
func NeedsRehash(phc string) bool {
	h, err := parse(phc)
	if err != nil {
		return true
	}
	return h.m != Memory || h.t != Iterations || h.p != Parallelism ||
		len(h.salt) != SaltLen || len(h.key) != KeyLen
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy fa il lavoro di una verifica contro un hash finto (stessi
// parametri correnti) e restituisce sempre false. Serve al login con utente
// inesistente, perché i tempi restino simili a quelli di una password errata.
func VerifyDummy(pw string) bool {
	dummyOnce.Do(func() {
		// La password del finto hash è casuale e non viene mai conservata.
		buf := make([]byte, 24)
		_, _ = rand.Read(buf)
		dummyHash, _ = Hash(string(buf))
	})
	_, _ = Verify(pw, dummyHash)
	return false
}
