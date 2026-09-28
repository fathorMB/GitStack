// Package tokens fornisce la generazione, la validazione, l'hashing e il
// parsing dei token personali di GitStack.
//
// Un token personale è una stringa con il formato:
//
//	gst_<43 caratteri base62>_<6 caratteri checksum>
//
// dove i 43 caratteri base62 derivano da 32 byte casuali letti da
// crypto/rand (62^43 > 2^256, quindi 43 caratteri bastano), e i 6
// caratteri di checksum sono la CRC32 di quei 32 byte codificata in
// base62. La lunghezza totale del token è 54 caratteri (gst_ + 43 + _ + 6).
//
// La validazione del formato (prefisso, lunghezza, separatore '_', checksum
// CRC32) avviene offline, senza interpellare il database. Per la ricerca
// e il confronto il token viene hashato con SHA-256 (32 byte grezzi) e
// l'hash binario viene salvato nel database: il token in chiaro non è mai
// restituito né loggato dalle funzioni di hashing.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/big"
	"sort"
	"strings"
)

const (
	prefix = "gst_"

	// payloadLen è il numero di caratteri base62 per 32 byte:
	// 62^43 > 2^256, quindi 43 caratteri bastano.
	payloadLen = 43

	// checksumLen è il numero di caratteri base62 per CRC32:
	// 62^6 > 2^32, quindi 6 caratteri bastano.
	checksumLen = 6

	// tokenLen è la lunghezza totale: prefisso(4) + payload(43) + separator(1) + checksum(6) = 54
	tokenLen = len(prefix) + payloadLen + 1 + checksumLen
)

var (
	bigSixtyTwo = big.NewInt(62)

	// ErrInvalidToken è l'errore restituito da Validate quando il token
	// non ha il formato corretto (prefisso, lunghezza, separatore, o
	// caratteri invalidi nel payload/checksum).
	ErrInvalidToken = errors.New("token non valido: formato errato")

	// ErrChecksumMismatch è l'errore restituito quando il checksum
	// CRC32 del payload non corrisponde.
	ErrChecksumMismatch = errors.New("token non valido: checksum errato")
)

// base62Alphabet è l'alfabeto base62 usato per codifica/decodifica.
const base62Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// buildScopeMap crea una mappa da scope stringa a bool per lookup rapido.
func buildScopeMap(scopes []string) map[string]bool {
	m := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		m[s] = true
	}
	return m
}

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

// Generate produce un nuovo token personale.
//
// Genera 32 byte da crypto/rand, li codifica in base62 (43 caratteri),
// calcola il checksum CRC32 in base62 (6 caratteri) e restituisce la
// stringa completa "gst_<payload>_<checksum>".
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
// interrogare il database: verifica prefisso, lunghezza, separatore '_',
// caratteri validi nel payload e nel checksum, e checksum CRC32.
//
// Ritorna true se il token è formalmente valido, false altrimenti.
// Usa il pattern go-error-wrapping per restituire un errore con
// informazioni su qual è il problema, ma per compatibilità con il
// codice esistente mantiene un ritorno bool e offre anche un errore.
func Validate(token string) bool {
	err := validate(token)
	return err == nil
}

// validate fa il lavoro vero di Validate, restituendo l'errore.
// Solo per uso interno; Validate lo avvolge in un bool.
func validate(token string) error {
	if len(token) != tokenLen {
		return ErrInvalidToken
	}
	if !strings.HasPrefix(token, prefix) {
		return ErrInvalidToken
	}

	// Verifica del separatore '_' alla posizione esatta.
	sepPos := len(prefix) + payloadLen
	if token[sepPos] != '_' {
		return ErrInvalidToken
	}

	// Verifica che il checksum sia alla fine.
	checksumPos := sepPos + 1
	if checksumPos+checksumLen != tokenLen {
		return ErrInvalidToken
	}

	// Estrae payload e checksum.
	payload := token[len(prefix) : len(prefix)+payloadLen]
	checksum := token[checksumPos:]

	// Decodifica il payload, validando i caratteri base62.
	raw, err := base62Decode(payload)
	if err != nil {
		return err
	}

	// Verifica checksum CRC32.
	expected := base62Checksum(raw)
	if expected != checksum {
		return ErrChecksumMismatch
	}

	return nil
}

// HashBytes restituisce l'hash SHA-256 grezzo del token come [32]byte.
// Questo è il valore da salvare in api_tokens.token_hash (BYTEA, octet_length = 32).
// Il token in chiaro non viene mai loggato o restituito da altre funzioni.
func HashBytes(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

// Hash calcola l'hash SHA-256 del token completo, restituito in
// esadecimale (lowercase). L'hash è deterministico: lo stesso token
// produce sempre lo stesso hash.
//
// Il token in chiaro non viene mai loggato o restituito.
func Hash(token string) string {
	h := HashBytes(token)
	return fmt.Sprintf("%x", h)
}

// CompareToken confronta un token in chiaro con il suo hash salvato
// nel database (32 byte grezzi, BYTEA). Usa un confronto a tempo
// costante per prevenire side-channel attack.
//
// Se l'hash non ha esattamente 32 byte, ritorna false.
func CompareToken(token, hashStr string) bool {
	if len(hashStr) != 32 {
		return false
	}
	tokenHash := HashBytes(token)
	return subtle.ConstantTimeCompare(tokenHash[:], []byte(hashStr)) == 1
}

// ParseScopes analizza una lista di scope stringa e restituisce una
// lista ordinata di scope validi.
//
// Restituisce ErrUnknownScope se trova uno scope non nel catalogo,
// ErrDuplicateScope se trova duplicati. Restituisce la lista
// normalizzata (ordine alfabetico) in caso di successo.
var (
	ErrUnknownScope  = errors.New("scope sconosciuto")
	ErrDuplicateScope = errors.New("scope duplicato")
)

func ParseScopes(input []string) ([]string, error) {
	known := buildScopeMap(KnownScopes)
	seen := make(map[string]bool)
	result := make([]string, 0, len(input))

	for _, s := range input {
		if !known[s] {
			return nil, fmt.Errorf("%w %q: %s", ErrUnknownScope, s, "scope non nel catalogo")
		}
		if seen[s] {
			return nil, fmt.Errorf("%w %q: %s", ErrDuplicateScope, s, "scope già presente")
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
// La stringa risultante è sempre di 43 caratteri (padding con 'A' a sinistra se necessario).
func base62Encode(data []byte) string {
	num := new(big.Int).SetBytes(data)

	var buf strings.Builder
	rem := new(big.Int)
	for num.Sign() > 0 {
		num.DivMod(num, bigSixtyTwo, rem)
		buf.WriteByte(base62Alphabet[rem.Int64()])
	}

	s := reverse(buf.String())
	// Padding con 'A' (valore 0 in base62) a sinistra fino a 43 caratteri.
	for len(s) < payloadLen {
		s = "A" + s
	}
	return s
}

// base62Decode decodifica una stringa base62 in byte slice.
// Restituisce un errore se trova caratteri non validi nell'alfabeto base62.
func base62Decode(s string) ([]byte, error) {
	// Costruisce la mappa valore-carattere per lookup rapido.
	val := make(map[byte]int64, len(base62Alphabet))
	for i, c := range base62Alphabet {
		val[byte(c)] = int64(i)
	}

	num := new(big.Int)
	for i := range s {
		v, ok := val[s[i]]
		if !ok {
			return nil, ErrInvalidToken
		}
		num.Mul(num, bigSixtyTwo)
		num.Add(num, big.NewInt(v))
	}

	raw := num.Bytes()
	// Pad con zeri leading per arrivare a 32 byte.
	if len(raw) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(raw):], raw)
		return padded, nil
	}
	return raw, nil
}

// base62Checksum calcola il checksum CRC32 dei byte e lo codifica
// in base62, restituendo esattamente 6 caratteri.
func base62Checksum(data []byte) string {
	checksum := crc32.ChecksumIEEE(data)
	val := uint64(checksum)

	var buf [checksumLen]byte

	for i := checksumLen - 1; i >= 0; i-- {
		buf[i] = base62Alphabet[val%62]
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
