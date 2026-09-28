// Package tokens fornisce la generazione, la validazione, l'hashing e il
// parsing dei token personali di GitStack.
//
// Un token personale è una stringa con il formato:
//
//	gst_<64 caratteri base62>_<6 caratteri checksum>
//
// dove i 64 caratteri base62 derivano da 32 byte casuali letti da
// crypto/rand, e i 6 caratteri di checksum sono la CRC32 di quei 32 byte
// codificata in base62 (6 caratteri ≃ 32 bit).
//
// La validazione del formato (prefisso, lunghezza, checksum) avviene
// offline, senza interpellare il database. Per la ricerca e il confronto
// il token viene hashato con SHA-256 e l'hash (in hex) viene salvato nel
// database: il token in chiaro non è mai restituito né loggato dalle
// funzioni di hashing.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"hash/crc32"
	"io"
	"math/big"
	"sort"
	"strings"
)

const (
	prefix = "gst_"
	payloadLen = 64
	checksumLen = 6
	tokenLen = len(prefix) + payloadLen + 1 + checksumLen // 75
)

var (
	bigSixtyTwo = big.NewInt(62)
)

// KnownScopes è il catalogo degli scope definiti in M-02/D (identico a
// GIT-29). Un scope è una stringa di forma "<azione>:<risorsa>".
var KnownScopes = []string{
	"read:user",
	"write:user",
	"read:org",
	"write:org",
	"admin:org",
	"read:resource",
	"write:resource",
}

// scopeImplications contiene le regole di implicazione tra scope:
//
//	write:X implica read:X
//	admin:org implica write:org e read:org
var scopeImplications = map[string][]string{
	"write:user":     {"read:user"},
	"write:org":      {"read:org"},
	"admin:org":      {"write:org", "read:org"},
	"write:resource": {"read:resource"},
}

// buildScopeMap crea una mappa da scope stringa a bool per lookup rapido.
func buildScopeMap(scopes []string) map[string]bool {
	m := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		m[s] = true
	}
	return m
}

// Generate produce un nuovo token personale.
//
// Genera 32 byte da crypto/rand, li codifica in base62, calcola il checksum
// CRC32 in base62 (6 caratteri) e restituisce la stringa completa
// "gst_<payload>_<checksum>".
//
// È l'unica funzione del package che restituisce il token in chiaro.
func Generate() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("lettura crypto/rand: %w", err)
	}

	payload := base62Encode(raw)
	checksum := base62Checksum(raw)

	return prefix + payload + "_" + checksum, nil
}

// Validate controlla che il token rispetti il formato noto senza
// interrogare il database: verifica prefisso, lunghezza e checksum.
//
// Ritorna true se il token è formalmente valido (prefisso corretto,
// lunghezza 74, checksum CRC32 corrispondente), false altrimenti.
func Validate(token string) bool {
	if !strings.HasPrefix(token, prefix) {
		return false
	}
	expectedLen := len(prefix) + 64 + 1 + 6 // gst_ + 64 + _ + 6
	if len(token) != expectedLen {
		return false
	}

	raw := base62Decode(token[len(prefix) : len(prefix)+64])
	if len(raw) != 32 {
		return false
	}

	expected := base62Checksum(raw)
	actual := token[len(token)-6:]
	return expected == actual
}

// Hash calcola l'hash SHA-256 del token completo, restituito in
// esadecimale (lowercase). L'hash è deterministico: lo stesso token
// produce sempre lo stesso hash.
//
// Il token in chiaro non viene mai loggato o restituito.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}

// CompareToken confronta un token in chiaro con il suo hash
// memorizzato nel database, usando un confronto a tempo costante
// per prevenire side-channel attack.
func CompareToken(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(token)), []byte(hash)) == 1
}

// ParseScopes analizza una lista di scope stringa e restituisce una
// lista ordinata di scope validi.
//
// Rifiuta scope sconosciuti e duplicati, restituendo un errore.
// La lista restituita è in ordine alfabetico (normalizzazione).
func ParseScopes(input []string) ([]string, error) {
	known := buildScopeMap(KnownScopes)
	seen := make(map[string]bool)
	var result []string

	for _, s := range input {
		if !known[s] {
			return nil, fmt.Errorf("scope sconosciuto %q", s)
		}
		if seen[s] {
			return nil, fmt.Errorf("scope duplicato %q", s)
		}
		seen[s] = true
		result = append(result, s)
	}

	sort.Strings(result)
	return result, nil
}

// Satisfies verifica se l'insieme di scope forniti (have) soddisfa
// lo scope richiesto (need), considerando le implicazioni definite:
//
//	write:X implica read:X
//	admin:org implica write:org e read:org
//
// Ritorna true se:
//   - have contiene esattamente need, oppure
//   - have contiene uno scope che implica need (ricorsivamente).
func Satisfies(have []string, need string) bool {
	haveMap := buildScopeMap(have)

	// Caso base: lo scope è direttamente presente.
	if haveMap[need] {
		return true
	}

	// Cerca se uno degli scope in have implica need.
	for _, s := range have {
		if implies(s, need, haveMap) {
			return true
		}
	}
	return false
}

// implies verifica che lo scope s implichi target, seguendo la catena
// di implicazioni (anche transitiva).
func implies(s, target string, haveMap map[string]bool) bool {
	implications := scopeImplications[s]
	for _, imp := range implications {
		if imp == target {
			return true
		}
		if haveMap[imp] && implies(imp, target, haveMap) {
			return true
		}
	}
	return false
}

// base62Encode codifica byte slice in stringa base62.
// L'alfabeto base62 usato: A-Z, a-z, 0-9.
// La stringa è sempre di 64 caratteri (padding con 'A' a sinistra se necessario).
func base62Encode(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	num := new(big.Int).SetBytes(data)

	var buf strings.Builder
	rem := new(big.Int)
	for num.Sign() > 0 {
		num.DivMod(num, bigSixtyTwo, rem)
		buf.WriteByte(alphabet[rem.Int64()])
	}

	s := reverse(buf.String())
	// Padding con 'A' (valore 0 in base62) a sinistra fino a 64 caratteri.
	for len(s) < 64 {
		s = "A" + s
	}
	return s
}

// base62Decode decodifica una stringa base62 in byte slice.
// La stringa di input deve avere esattamente 64 caratteri.
func base62Decode(s string) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	// Mappa carattere → valore.
	val := make(map[byte]int64, len(alphabet))
	for i, c := range alphabet {
		val[byte(c)] = int64(i)
	}

	num := new(big.Int)
	for i := range s {
		v := val[s[i]]
		num.Mul(num, bigSixtyTwo)
		num.Add(num, big.NewInt(v))
	}

	raw := num.Bytes()
	// Pad con zeri leading per arrivare a 32 byte.
	if len(raw) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(raw):], raw)
		return padded
	}
	return raw
}

// base62Checksum calcola il checksum CRC32 dei byte e lo codifica
// in base62, restituendo esattamente 6 caratteri.
func base62Checksum(data []byte) string {
	checksum := crc32.ChecksumIEEE(data)
	// CRC32 = 32 bit unsigned, codifica in base62 (6 caratteri).
	val := uint64(checksum)

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var buf [6]byte

	for i := 5; i >= 0; i-- {
		buf[i] = alphabet[val%62]
		val /= 62
	}

	return string(buf[:])
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
