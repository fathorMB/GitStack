package webhooks

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/fathorMB/GitStack/pkg/egress"
)

// NewEgress costruisce il client delle consegne (pkg/egress, C8): l'unico modo
// di uscire. Le liste vengono dai values del chart (egress.*). Voci non valide
// fanno fallire l'avvio.
func NewEgress(allow, deny, clusterCIDRs []string) (Doer, URLChecker, error) {
	policy, err := egress.NewPolicy(egress.Config{Allow: allow, Deny: deny, ClusterCIDRs: clusterCIDRs})
	if err != nil {
		return nil, nil, fmt.Errorf("webhooks: policy di uscita non valida: %w", err)
	}
	return egress.NewClient(policy, egress.Options{Timeout: RequestTimeout}), CheckURL(policy), nil
}

// URLChecker valida l'indirizzo di un webhook alla creazione.
type URLChecker func(raw string) error

// ErrURLNotAllowed: l'indirizzo non è ammesso (422 url_not_allowed).
var ErrURLNotAllowed = errors.New("indirizzo non ammesso")

// CheckURL rifiuta ciò che si vede già dal testo: schema diverso da http e
// https, host mancante, localhost e IP letterali bloccati dalla policy. I nomi
// non si risolvono qui (cambiano nel tempo): la protezione vera è nel dialer,
// a ogni consegna e a ogni redirect (C8).
func CheckURL(policy *egress.Policy) URLChecker {
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return fmt.Errorf("%w: serve un indirizzo http o https", ErrURLNotAllowed)
		}
		if u.User != nil {
			return fmt.Errorf("%w: niente credenziali nell'indirizzo", ErrURLNotAllowed)
		}
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return fmt.Errorf("%w: localhost è bloccato", ErrURLNotAllowed)
		}
		if ip, err := netip.ParseAddr(host); err == nil && policy != nil {
			if err := policy.CheckIP(ip); err != nil {
				return fmt.Errorf("%w: %s è bloccato", ErrURLNotAllowed, host)
			}
		}
		return nil
	}
}
