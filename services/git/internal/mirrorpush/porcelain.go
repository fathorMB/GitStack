package mirrorpush

import "strings"

// Line è una riga dell'output `git push --porcelain`.
type Line struct {
	Flag   byte
	From   string
	To     string
	Status string // StatusOK, StatusUpToDate, StatusRejected, StatusError
	// Summary è il testo fra la seconda tabulazione e il motivo, es.
	// `abc..def` o `[new branch]`.
	Summary string
	// Reason è il motivo fra parentesi: `non-fast-forward`, `fetch first`,
	// `already exists`, un messaggio del remote...
	Reason string
}

// Diverged: il rifiuto dice che la destinazione ha una storia diversa.
func (l Line) Diverged() bool {
	switch l.Reason {
	case "non-fast-forward", "fetch first", "already exists", "stale info":
		return true
	}
	return false
}

// ParsePorcelain legge l'output di `git push --porcelain`: righe
// `<flag>\t<da>:<a>\t<riepilogo> (<motivo>)`, precedute da `To <url>` e
// chiuse da `Done`. Le righe che non hanno quel formato si ignorano.
//
// Flag: ' ' push riuscito (fast-forward), '*' ref nuovo, '=' già aggiornato,
// '!' rifiutato o fallito, '+' aggiornamento forzato, '-' cancellazione.
// Il push è sempre non forzato e senza cancellazioni: un '+' o un '-' non
// dovrebbero comparire mai e, se compaiono, sono un errore (StatusError),
// mai un successo silenzioso.
func ParsePorcelain(out string) []Line {
	var lines []Line
	for _, raw := range strings.Split(out, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if len(raw) < 2 || raw[1] != '\t' {
			continue
		}
		parts := strings.SplitN(raw[2:], "\t", 2)
		if len(parts) != 2 {
			continue
		}
		from, to, ok := strings.Cut(parts[0], ":")
		if !ok {
			continue
		}
		l := Line{Flag: raw[0], From: from, To: to}
		l.Summary, l.Reason = splitReason(parts[1])
		switch l.Flag {
		case ' ', '*':
			l.Status = StatusOK
		case '=':
			l.Status = StatusUpToDate
		case '!':
			l.Status = StatusRejected
			if strings.HasPrefix(l.Summary, "[remote failure]") {
				l.Status = StatusError
			}
		default:
			l.Status = StatusError
			if l.Reason == "" {
				l.Reason = "esito inatteso del push (flag " + string(l.Flag) + ")"
			}
		}
		lines = append(lines, l)
	}
	return lines
}

// splitReason separa `[rejected] (non-fast-forward)` in riepilogo e motivo.
func splitReason(s string) (summary, reason string) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ")") {
		if i := strings.LastIndex(s, " ("); i >= 0 {
			return strings.TrimSpace(s[:i]), s[i+2 : len(s)-1]
		}
	}
	return s, ""
}
