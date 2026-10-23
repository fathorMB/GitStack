package ci

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// MaskedPlaceholder sostituisce un segreto nei log.
const MaskedPlaceholder = "***"

// minMaskLen: sotto questa lunghezza un valore non si maschera (un segreto di un
// carattere cancellerebbe quel carattere da tutto il log). I valori di pochi
// caratteri sono comunque mascherati dal runner con ::add-mask::.
const minMaskLen = 4

// Masker toglie dai log i segreti di un job, anche codificati in modo banale:
// in chiaro, in base64 (standard, URL, con e senza padding, a tutti e tre gli
// allineamenti), in percentuale (URL), in esadecimale e al contrario.
// Una riga di un segreto su più righe si maschera per riga.
type Masker struct {
	// più lunghe prima: «abcd» non deve mangiare metà di «abcdef».
	needles []string
}

// NewMasker costruisce il masker dei valori dati (i vuoti e i troppo corti si ignorano).
func NewMasker(values ...string) *Masker {
	set := map[string]struct{}{}
	add := func(s string) {
		if len(s) >= minMaskLen {
			set[s] = struct{}{}
		}
	}
	for _, v := range values {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' }) {
			part = strings.TrimSpace(part)
			if len(part) < minMaskLen {
				continue
			}
			for _, form := range variants(part) {
				add(form)
			}
		}
		if t := strings.TrimSpace(v); t != "" {
			for _, form := range variants(t) {
				if !strings.ContainsAny(form, "\r\n") {
					add(form)
				}
			}
		}
	}
	m := &Masker{}
	for s := range set {
		m.needles = append(m.needles, s)
	}
	sort.Slice(m.needles, func(i, j int) bool {
		if len(m.needles[i]) != len(m.needles[j]) {
			return len(m.needles[i]) > len(m.needles[j])
		}
		return m.needles[i] < m.needles[j]
	})
	return m
}

func variants(s string) []string {
	out := []string{s, url.QueryEscape(s), url.PathEscape(s), hex.EncodeToString([]byte(s)), strings.ToUpper(hex.EncodeToString([]byte(s)))}
	out = append(out, b64Forms(s)...)
	out = append(out, reverse(s))
	// Il valore percentualizzato di nuovo (§ query dentro query) e in base64 del base64.
	out = append(out, b64Forms(base64.StdEncoding.EncodeToString([]byte(s)))...)
	return out
}

// b64Forms: la forma base64 del segreto dentro un testo più lungo dipende dall'allineamento
// (offset 0, 1 o 2 byte di prefisso): si tengono le parti stabili di ognuno.
func b64Forms(s string) []string {
	var out []string
	// Caratteri iniziali che dipendono dai byte di prefisso, per prefisso di 0, 1 e 2 byte.
	skips := [3]int{0, 2, 3}
	for pad := 0; pad < 3; pad++ {
		raw := []byte(strings.Repeat("\x00", pad) + s)
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
			e := enc.EncodeToString(raw)
			if skips[pad] >= len(e) {
				continue
			}
			t := strings.TrimRight(e[skips[pad]:], "=")
			out = append(out, t)
			// La coda dipende da ciò che segue il segreto: si tiene anche il corpo senza gli ultimi 3 caratteri.
			if len(t) >= 8+3 {
				out = append(out, t[:len(t)-3])
			}
		}
	}
	return out
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// Mask sostituisce ogni occorrenza di un segreto.
func (m *Masker) Mask(line string) string {
	if m == nil || len(m.needles) == 0 {
		return line
	}
	for _, n := range m.needles {
		if strings.Contains(line, n) {
			line = strings.ReplaceAll(line, n, MaskedPlaceholder)
		}
	}
	return line
}
