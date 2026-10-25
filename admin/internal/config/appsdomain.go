package config

import (
	"errors"
	"net"
	"strings"
)

// MaxAppsDomainLen lascia posto a <app>-<repo> di 63 caratteri e al punto:
// la stessa regola di pkg/names.ValidateAppsDomain e di install.sh.
const MaxAppsDomainLen = 253 - 63 - 1

// ValidateAppsDomain controlla il dominio delle app (H2): nome DNS in
// minuscolo con almeno due etichette (lettere, cifre, '-', senza '-' ai
// bordi, al massimo 63 caratteri ciascuna), non un IP.
func ValidateAppsDomain(d string) error {
	if d == "" || len(d) > MaxAppsDomainLen {
		return errors.New("serve un nome DNS di al massimo 189 caratteri")
	}
	if net.ParseIP(d) != nil {
		return errors.New("deve essere un nome DNS, non un indirizzo IP")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return errors.New("servono almeno due etichette, per esempio apps.azienda.it")
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return errors.New("ogni etichetta ha 1-63 caratteri e non inizia né finisce con '-'")
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return errors.New("sono ammessi solo lettere minuscole, cifre e '-'")
			}
		}
	}
	return nil
}
