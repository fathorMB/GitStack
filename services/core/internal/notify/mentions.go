package notify

import (
	"regexp"
	"strings"
)

var (
	fencedCode = regexp.MustCompile("(?s)```.*?(```|$)|~~~.*?(~~~|$)")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	// @utente o @org/team: il carattere prima non è parte di una parola, di
	// un indirizzo email, di un percorso o di un'altra menzione.
	mentionRe = regexp.MustCompile(`(^|[^A-Za-z0-9_@/.\-])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?(?:/[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?)?)`)
)

// ParseMentions ritorna i nomi citati nel testo (`utente` o `org/team`, senza
// la chiocciola), in minuscolo, senza doppioni e nell'ordine di apparizione.
// Il codice (blocchi e `inline`) non menziona nessuno (I8: solo testo).
func ParseMentions(text string) []string {
	text = fencedCode.ReplaceAllString(text, " ")
	text = inlineCode.ReplaceAllString(text, " ")
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[2])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// newMentions sono i nomi di text che non c'erano in prev: una modifica
// notifica solo chi viene menzionato per la prima volta.
func newMentions(text, prev string) []string {
	before := map[string]bool{}
	for _, n := range ParseMentions(prev) {
		before[n] = true
	}
	var out []string
	for _, n := range ParseMentions(text) {
		if !before[n] {
			out = append(out, n)
		}
	}
	return out
}
