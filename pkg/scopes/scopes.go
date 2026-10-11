// Package scopes è l'unica fonte della regola di implicazione tra scope dei
// token personali (catalogo TokenScope del contratto API). La usano il
// gateway (API), identity (verifica dei token) e git (HTTPS e SSH): nessun
// servizio ne tiene una copia locale, così non possono divergere.
//
// Regole:
//
//	write:X implica read:X
//	admin:X implica write:X e read:X
package scopes

import "strings"

// Implications è il catalogo delle implicazioni: per ogni scope, quelli che
// concede in più. Le chiavi sono gli scope noti del contratto; la regola
// generica di Grants (write:X→read:X, admin:X→write:X/read:X) è la stessa.
var Implications = map[string][]string{
	"write:user":     {"read:user"},
	"write:org":      {"read:org"},
	"admin:org":      {"write:org", "read:org"},
	"write:resource": {"read:resource"},
}

// Grants dice se l'insieme di scope del token (have) soddisfa lo scope
// richiesto: lo contiene, oppure uno scope che lo implica.
func Grants(have []string, required string) bool {
	for _, h := range have {
		if h == required || implies(h, required) {
			return true
		}
	}
	return false
}

// Missing ritorna gli scope richiesti che l'insieme non soddisfa.
func Missing(have, required []string) []string {
	var miss []string
	for _, r := range required {
		if !Grants(have, r) {
			miss = append(miss, r)
		}
	}
	return miss
}

func implies(have, required string) bool {
	hAct, hScope, ok := strings.Cut(have, ":")
	rAct, rScope, ok2 := strings.Cut(required, ":")
	if !ok || !ok2 || hScope != rScope {
		return false
	}
	switch hAct {
	case "admin":
		return rAct == "write" || rAct == "read"
	case "write":
		return rAct == "read"
	}
	return false
}
