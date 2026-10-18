package password

import (
	_ "embed" // elenco di password comuni incorporato nel binario
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed common_passwords.txt
var commonPasswordsTxt string

var (
	commonOnce sync.Once
	commonSet  map[string]struct{}
)

// commonPasswords carica una volta l'elenco incorporato (minuscole, solo le
// voci lunghe almeno MinLength: le più corte il minimo le rifiuta già).
func commonPasswords() map[string]struct{} {
	commonOnce.Do(func() {
		commonSet = make(map[string]struct{})
		for _, line := range strings.Split(commonPasswordsTxt, "\n") {
			line = strings.ToLower(strings.TrimSpace(line))
			if line == "" || strings.HasPrefix(line, "#") || utf8.RuneCountInString(line) < MinLength {
				continue
			}
			commonSet[line] = struct{}{}
		}
	})
	return commonSet
}

// IsCommon dice se pw è una password comune: nell'elenco incorporato (senza
// distinguere maiuscole, anche con una coda di cifre o punteggiatura
// semplice) oppure fatta di un solo carattere ripetuto o di una sola
// sequenza breve ripetuta (es. "abcabcabcabc").
func IsCommon(pw string) bool {
	p := strings.ToLower(strings.TrimSpace(pw))
	set := commonPasswords()
	if _, ok := set[p]; ok {
		return true
	}
	const symbols = "!@#$%^&*.?_-"
	for _, cut := range []string{symbols, symbols + "0123456789"} {
		if base := strings.TrimRight(p, cut); base != p {
			if _, ok := set[base]; ok {
				return true
			}
		}
	}
	return isRepeated(p)
}

// isRepeated: la stringa è un blocco di 1-4 caratteri ripetuto per intero.
func isRepeated(p string) bool {
	r := []rune(p)
	for size := 1; size <= 4 && size*2 <= len(r); size++ {
		if len(r)%size != 0 {
			continue
		}
		ok := true
		for i := size; i < len(r); i++ {
			if r[i] != r[i-size] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
