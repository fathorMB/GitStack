package names

import (
	"errors"
	"strings"
)

// Indirizzi automatici delle app (v3, H2): <app>-<repo>-<owner>.<apps-domain>,
// per esempio web-handbook-acme.apps.acme.it (ADR 0002, «Esposizione e TLS»).
//
// È un'unica etichetta DNS, così il certificato wildcard *.<apps-domain> della
// CA interna le copre tutte. Owner e repo ammettono '_', '.' e maiuscole (R11),
// che in un nome DNS non possono stare: diventano '-' e minuscole. L'owner
// nell'etichetta evita le collisioni fra repo omonimi di owner diversi. Non ci
// sono suffissi automatici: un'etichetta oltre 63 caratteri è rifiutata
// (ErrAddressTooLong, «address_too_long» nell'API) e un'etichetta già in uso
// da un'altra app è un conflitto (ErrAddressConflict, «address_conflict»; a
// garantirlo è l'indice unico su apps.normalized_host).

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
	ErrAppNameEmpty    = errors.New("app name must not be empty")
	ErrAppNameTooLong  = errors.New("app name must be at most 40 characters long")
	ErrAppNameChar     = errors.New("app name may only contain lowercase letters, digits and '-'")
	ErrAppNameDash     = errors.New("app name must not start or end with '-'")
	ErrAppsDomain      = errors.New("apps domain must be a lowercase DNS name with at least two labels (letters, digits, '-')")
	ErrAddressInvalid  = errors.New("owner and repository names must contain at least one letter or digit")
	ErrAddressTooLong  = errors.New("app address label must be at most 63 characters long")
	ErrAddressConflict = errors.New("app address already in use")
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

// AppAddress è l'indirizzo automatico <app>-<repo>-<owner>.<domain>. app e
// domain devono essere validi (ValidateAppName, ValidateAppsDomain); owner e
// repo sono i nomi come scritti (R11) e vengono normalizzati per il DNS.
// Restituisce ErrAddressTooLong se l'etichetta supera MaxDNSLabelLen.
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
	r, o := dnsSlug(repo), dnsSlug(owner)
	if r == "" || o == "" {
		return "", ErrAddressInvalid
	}
	label := app + "-" + r + "-" + o
	if len(label) > MaxDNSLabelLen {
		return "", ErrAddressTooLong
	}
	return label + "." + domain, nil
}

// CheckAppAddress calcola l'indirizzo come AppAddress e restituisce
// ErrAddressConflict se taken dice che è già in uso da un'altra app. Non
// inventa suffissi: app e repo con trattini possono dare la stessa etichetta
// (app «a-b» in «c» e app «a» in «b-c», stesso owner) e il secondo è rifiutato.
func CheckAppAddress(owner, repo, app, domain string, taken func(address string) bool) (string, error) {
	addr, err := AppAddress(owner, repo, app, domain)
	if err != nil {
		return "", err
	}
	if taken != nil && taken(addr) {
		return "", ErrAddressConflict
	}
	return addr, nil
}
