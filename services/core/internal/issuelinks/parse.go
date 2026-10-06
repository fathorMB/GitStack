package issuelinks

import (
	"regexp"
	"strconv"
	"strings"
)

// Keyword è la parola chiave di chiusura normalizzata (C2).
const (
	KeywordClose   = "close"
	KeywordFix     = "fix"
	KeywordResolve = "resolve"
)

// Ref è un riferimento a una issue nel messaggio di un commit (C1, C2).
type Ref struct {
	// Repo è "owner/repo" in minuscolo, vuoto per `#n` (repo del commit).
	Repo   string
	Number int64
	// Keyword è close|fix|resolve se il riferimento ha davanti una parola di
	// chiusura (close/closes/closed, fix/fixes/fixed, resolve/resolves/resolved,
	// senza distinzione di maiuscole), altrimenti vuoto.
	Keyword string
}

// refRe: `#n` o `owner/repo#n`. Il carattere prima non deve far parte di una
// parola (così `abc#1` e gli anchor degli URL non contano) e dopo il numero
// non devono seguire altre cifre o lettere.
var refRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_/#.\-])((?:[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+)?)#([0-9]{1,18})(?:[^A-Za-z0-9_]|$)`)

// keywordRe riconosce la parola di chiusura subito prima del riferimento
// (uno spazio, o due punti e spazio).
var keywordRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])(close[sd]?|fix(?:e[sd])?|resolve[sd]?):?[ \t]+$`)

// ParseRefs estrae i riferimenti del messaggio, nell'ordine in cui compaiono,
// senza duplicati: se lo stesso issue è citato più volte, la parola di
// chiusura vale se c'è almeno una volta.
func ParseRefs(msg string) []Ref {
	var out []Ref
	index := map[string]int{}
	pos := 0
	for pos <= len(msg) {
		m := refRe.FindStringSubmatchIndex(msg[pos:])
		if m == nil {
			break
		}
		repo := msg[pos+m[2] : pos+m[3]]
		numStart, numEnd := pos+m[4], pos+m[5]
		n, err := strconv.ParseInt(msg[numStart:numEnd], 10, 64)
		// Il prossimo giro riparte dal numero: il carattere dopo è già stato
		// consumato dal match ma può essere l'inizio di un altro riferimento.
		next := numEnd
		if next <= pos {
			next = pos + 1
		}
		if err == nil && n >= 1 {
			start := pos + m[2] // inizio di owner/repo o del '#'
			kw := ""
			if k := keywordRe.FindStringSubmatch(msg[:start]); k != nil {
				kw = normalizeKeyword(k[1])
			}
			key := strings.ToLower(repo) + "#" + strconv.FormatInt(n, 10)
			if i, ok := index[key]; ok {
				if out[i].Keyword == "" {
					out[i].Keyword = kw
				}
			} else {
				index[key] = len(out)
				out = append(out, Ref{Repo: strings.ToLower(repo), Number: n, Keyword: kw})
			}
		}
		pos = next
	}
	return out
}

func normalizeKeyword(w string) string {
	w = strings.ToLower(w)
	switch {
	case strings.HasPrefix(w, "close"):
		return KeywordClose
	case strings.HasPrefix(w, "fix"):
		return KeywordFix
	default:
		return KeywordResolve
	}
}
