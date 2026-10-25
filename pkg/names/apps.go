package names

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Indirizzi automatici delle app (v3, H2): <app>-<repo>.<apps-domain>.
//
// Lo schema è quello dell'ADR 0002 (bozza, item GIT-340): un'unica etichetta
// DNS, così il certificato wildcard *.<apps-domain> della CA interna le copre
// tutte. Il nome del repository ammette '_', '.' e maiuscole (R11), che in un
// nome DNS non possono stare: diventano '-' e minuscole. Due coppie diverse
// possono dare la stessa etichetta (app «a-b» in «c» e app «a» in «b-c»):
// UniqueAppAddress le distingue con un suffisso stabile.

const (
	// MaxAppNameLen è la lunghezza massima del nome di un'app.
	MaxAppNameLen = 40
	// MaxDNSLabelLen è il limite RFC 1035 di un'etichetta.
	MaxDNSLabelLen = 63
	// MaxHostLen è il limite di un nome DNS completo.
	MaxHostLen = 253
	// MaxAppsDomainLen lascia posto a un'etichetta di 63 caratteri e al punto.
	MaxAppsDomainLen = MaxHostLen - MaxDNSLabelLen - 1
)

// Errori di ValidateAppName, ValidateAppsDomain e UniqueAppAddress.
var (
	ErrAppNameEmpty   = errors.New("app name must not be empty")
	ErrAppNameTooLong = errors.New("app name must be at most 40 characters long")
	ErrAppNameChar    = errors.New("app name may only contain lowercase letters, digits and '-'")
	ErrAppNameDash    = errors.New("app name must not start or end with '-'")
	ErrAppsDomain     = errors.New("apps domain must be a lowercase DNS name with at least two labels (letters, digits, '-')")
	ErrNoFreeAddress  = errors.New("no free app address")
)

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// ValidateAppName controlla il nome di un'app: lettere minuscole, cifre e '-',
// da 1 a 40 caratteri, senza '-' all'inizio o alla fine.
func ValidateAppName(name string) error {
	if name == "" {
		return ErrAppNameEmpty
	}
	if len(name) > MaxAppNameLen {
		return ErrAppNameTooLong
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !isLower(c) && !isDigit(c) && c != '-' {
			return ErrAppNameChar
		}
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return ErrAppNameDash
	}
	return nil
}

// ValidateAppsDomain controlla il dominio delle app (come install.sh e
// `gitstack config set apps-domain`): nome DNS in minuscolo con almeno due
// etichette, ciascuna di 1-63 caratteri tra lettere, cifre e '-', senza '-' ai
// bordi; non un IPv4. Al massimo MaxAppsDomainLen caratteri.
func ValidateAppsDomain(domain string) error {
	if domain == "" || len(domain) > MaxAppsDomainLen {
		return ErrAppsDomain
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return ErrAppsDomain
	}
	allDigits := true
	for _, l := range labels {
		if l == "" || len(l) > MaxDNSLabelLen || l[0] == '-' || l[len(l)-1] == '-' {
			return ErrAppsDomain
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !isLower(c) && !isDigit(c) && c != '-' {
				return ErrAppsDomain
			}
			if !isDigit(c) {
				allDigits = false
			}
		}
	}
	if allDigits {
		return ErrAppsDomain
	}
	return nil
}

// dnsSlug riduce s a minuscole e cifre, con ogni altra sequenza di caratteri
// ridotta a un solo '-' e senza '-' ai bordi.
func dnsSlug(s string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if isLower(c) || isDigit(c) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteByte(c)
		} else {
			dash = true
		}
	}
	return b.String()
}

func hashOf(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(parts, "/"))))
	return hex.EncodeToString(sum[:])
}

// fitLabel aggiunge il suffisso (se c'è) e porta l'etichetta a
// MaxDNSLabelLen tagliando la parte iniziale, mai il suffisso.
func fitLabel(label, suffix string) string {
	if suffix != "" {
		suffix = "-" + suffix
	}
	if len(label)+len(suffix) <= MaxDNSLabelLen {
		return label + suffix
	}
	cut := MaxDNSLabelLen - len(suffix)
	return strings.TrimRight(label[:cut], "-") + suffix
}

// baseLabel è l'etichetta senza il suffisso di disambiguazione. Se supera
// il limite DNS è tagliata con un hash, così due nomi lunghi che differiscono
// solo dopo il taglio restano distinti.
func baseLabel(owner, repo, app string) string {
	slug := dnsSlug(repo)
	if slug == "" {
		slug = "repo"
	}
	label := app + "-" + slug
	if len(label) > MaxDNSLabelLen {
		return fitLabel(label, hashOf(owner, repo, app)[:8])
	}
	return label
}

// AppAddress è l'indirizzo automatico <app>-<repo>.<domain> senza controllo
// delle collisioni. app e domain devono essere validi (ValidateAppName,
// ValidateAppsDomain); repo è il nome del repository come scritto (R11).
func AppAddress(owner, repo, app, domain string) (string, error) {
	if err := ValidateAppName(app); err != nil {
		return "", err
	}
	if err := ValidateAppsDomain(domain); err != nil {
		return "", err
	}
	if err := ValidateRepoName(repo); err != nil {
		return "", err
	}
	return baseLabel(owner, repo, app) + "." + domain, nil
}

// UniqueAppAddress sceglie l'indirizzo dell'app evitando quelli già presi
// (taken dice se un indirizzo è in uso da un'altra app). Prima prova
// <app>-<repo>; se è occupato aggiunge un suffisso esadecimale ricavato da
// owner/repo/app, quindi stabile e diverso per ogni coppia, e se serve lo
// allunga. Con gli stessi indirizzi occupati lo stesso input dà sempre lo
// stesso indirizzo. Un'app che ha già il suo indirizzo lo conserva: il
// chiamante non chiede un indirizzo nuovo per un'app esistente.
func UniqueAppAddress(owner, repo, app, domain string, taken func(address string) bool) (string, error) {
	first, err := AppAddress(owner, repo, app, domain)
	if err != nil {
		return "", err
	}
	if taken == nil || !taken(first) {
		return first, nil
	}
	label := baseLabel(owner, repo, app)
	h := hashOf(owner, repo, app)
	for _, n := range []int{6, 8, 12, 16, 24, 32} {
		cand := fitLabel(label, h[:n]) + "." + domain
		if !taken(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%w for %s/%s app %s", ErrNoFreeAddress, owner, repo, app)
}
