package apps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Domini propri delle app pubbliche (v3-c, GIT-458, ADR 0002 «Controllo del DNS»). Questo file ha la
// normalizzazione dei nomi, il record DNS atteso e il controllo; il ciclo periodico e la rotta sono
// nel controller (domains_loop.go e ensureRoute).

// Tempi del controllo (ADR 0002): un dominio che non è ancora verified si ricontrolla ogni 5 minuti
// per la prima ora, poi ogni 30 minuti fino al giorno, poi ogni 6 ore; un verified ogni 10 minuti.
// Un verified che non punta più all'istanza passa in errore dopo il periodo di grazia (default 1 ora).
const (
	DefaultDomainGrace   = time.Hour
	domainVerifiedEvery  = 10 * time.Minute
	domainFirstEvery     = 5 * time.Minute
	domainSecondEvery    = 30 * time.Minute
	domainLateEvery      = 6 * time.Hour
	domainLookupTimeout  = 5 * time.Second
	maxDomainLen         = 253
	maxDomainLabelLength = 63
)

// Errori di validazione del nome (il codice HTTP li distingue).
var (
	ErrDomainInvalid  = errors.New("nome di dominio non valido")
	ErrDomainReserved = errors.New("dominio riservato")
)

// NormalizeDomain porta il nome in forma canonica (minuscolo, senza punto finale) e lo valida: nome DNS
// RFC 1123 con almeno due etichette, senza wildcard, schema o porta. Un indirizzo IP è riservato.
// instanceHost e appsDomain (anche vuoti) sono i domini di GitStack e delle app: il nome non può
// coincidere con loro né stare sotto di loro.
func NormalizeDomain(in, instanceHost, appsDomain string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in)), ".")
	if d == "" {
		return "", fmt.Errorf("%w: il nome è vuoto", ErrDomainInvalid)
	}
	if _, err := netip.ParseAddr(strings.Trim(d, "[]")); err == nil {
		return "", fmt.Errorf("%w: un indirizzo IP non è un dominio", ErrDomainReserved)
	}
	if strings.ContainsAny(d, "*/:@? #") {
		return "", fmt.Errorf("%w: senza schema, porta, percorso o wildcard", ErrDomainInvalid)
	}
	if len(d) > maxDomainLen {
		return "", fmt.Errorf("%w: al massimo %d caratteri", ErrDomainInvalid, maxDomainLen)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%w: serve un nome completo (per esempio shop.esempio.it)", ErrDomainInvalid)
	}
	for _, l := range labels {
		if l == "" || len(l) > maxDomainLabelLength || l[0] == '-' || l[len(l)-1] == '-' {
			return "", fmt.Errorf("%w: etichetta «%s» non ammessa", ErrDomainInvalid, l)
		}
		for _, r := range l {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", fmt.Errorf("%w: carattere «%c» non ammesso (RFC 1123)", ErrDomainInvalid, r)
			}
		}
	}
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return "", fmt.Errorf("%w: l'ultima etichetta non può essere numerica", ErrDomainInvalid)
	}
	for _, reserved := range []struct{ host, what string }{{instanceHost, "di GitStack"}, {appsDomain, "delle app"}} {
		h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(reserved.host)), ".")
		if h != "" && (d == h || strings.HasSuffix(d, "."+h)) {
			return "", fmt.Errorf("%w: %s è il dominio %s o ne è un sotto-dominio", ErrDomainReserved, d, reserved.what)
		}
	}
	return d, nil
}

// DomainResolver è il DNS con cui si controllano i domini; *net.Resolver lo soddisfa e i test ne
// passano uno finto.
type DomainResolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DomainConfig è la configurazione del controllo: dove sta l'istanza e come si risolve il DNS.
type DomainConfig struct {
	// AppsDomain: dominio delle app; l'indirizzo automatico è <label>.<AppsDomain>, il bersaglio del CNAME.
	AppsDomain string
	// Resolver: nil usa net.DefaultResolver (lookup diretto, senza cache propria).
	Resolver DomainResolver
	// Policy di pkg/egress: un dominio che risolve su un indirizzo interno (loopback, cluster, nodo) è
	// segnalato come tale nel motivo dell'errore. Nil usa i default.
	Policy *egress.Policy
	// Grace: per quanto un dominio verified che non punta più all'istanza resta verified (default 1 ora).
	Grace time.Duration
}

func (c DomainConfig) grace() time.Duration {
	if c.Grace > 0 {
		return c.Grace
	}
	return DefaultDomainGrace
}

func (c DomainConfig) resolver() DomainResolver {
	if c.Resolver != nil {
		return c.Resolver
	}
	return net.DefaultResolver
}

// DomainTarget è il bersaglio del CNAME di un'app: il suo indirizzo automatico.
func (c DomainConfig) DomainTarget(addressLabel *string) string {
	if addressLabel == nil || c.AppsDomain == "" {
		return ""
	}
	return *addressLabel + "." + strings.TrimSuffix(c.AppsDomain, ".")
}

// DNSRecord è il record da creare presso il provider del dominio.
type DNSRecord struct {
	Type, Name, Value string
	Alternatives      []DNSRecordAlt
}

// DNSRecordAlt è un record equivalente accettato dal controllo.
type DNSRecordAlt struct{ Type, Value string }

// IsApex: all'apice il CNAME non è ammesso. Senza la lista dei suffissi pubblici si considera apice
// un nome di due etichette (retail.it); per i suffissi a due livelli (co.uk) si usa A/AAAA o un
// CNAME «appiattito» del provider, e il controllo accetta entrambe le forme.
func IsApex(domain string) bool { return strings.Count(domain, ".") == 1 }

// ExpectedRecord dà il record atteso di un dominio: CNAME verso l'indirizzo automatico, oppure A/AAAA
// verso gli IP pubblici dichiarati se il dominio è all'apice e ce ne sono. Gli altri IP dichiarati
// sono alternative equivalenti (nella forma CNAME sono le A/AAAA accettate al posto del CNAME).
func (c DomainConfig) ExpectedRecord(domain string, addressLabel *string, publicIPs []string) DNSRecord {
	ips := append([]string(nil), publicIPs...)
	sort.Strings(ips)
	typeOf := func(ip string) string {
		if a, err := netip.ParseAddr(ip); err == nil && a.Is6() && !a.Is4In6() {
			return "AAAA"
		}
		return "A"
	}
	if IsApex(domain) && len(ips) > 0 {
		rec := DNSRecord{Type: typeOf(ips[0]), Name: domain, Value: ips[0], Alternatives: []DNSRecordAlt{}}
		for _, ip := range ips[1:] {
			rec.Alternatives = append(rec.Alternatives, DNSRecordAlt{Type: typeOf(ip), Value: ip})
		}
		return rec
	}
	rec := DNSRecord{Type: "CNAME", Name: domain, Value: c.DomainTarget(addressLabel), Alternatives: []DNSRecordAlt{}}
	for _, ip := range ips {
		rec.Alternatives = append(rec.Alternatives, DNSRecordAlt{Type: typeOf(ip), Value: ip})
	}
	return rec
}

// DNSResult è l'esito di un controllo del DNS.
type DNSResult struct {
	Status string // store.DomainVerified | DomainPendingDNS | DomainError
	Reason string // motivo leggibile, vuoto se verified o pending
}

// CheckDNS risolve il dominio e lo confronta col record atteso (ADR 0002): il CNAME che finisce
// sull'indirizzo automatico, oppure un insieme di A/AAAA contenuto negli IP pubblici dichiarati.
// Nessun record (NXDOMAIN, nessuna risposta, timeout) = pending_dns; record diversi = error col motivo.
func (c DomainConfig) CheckDNS(ctx context.Context, domain string, addressLabel *string, publicIPs []string) DNSResult {
	ctx, cancel := context.WithTimeout(ctx, domainLookupTimeout)
	defer cancel()
	res := c.resolver()
	target := strings.ToLower(c.DomainTarget(addressLabel))

	cname, cnameErr := res.LookupCNAME(ctx, domain)
	cname = strings.TrimSuffix(strings.ToLower(cname), ".")
	if cnameErr == nil && target != "" && cname == target {
		return DNSResult{Status: store.DomainVerified}
	}
	addrs, hostErr := res.LookupHost(ctx, domain)
	if hostErr != nil || len(addrs) == 0 {
		// Nessun record, oppure un guasto del resolver (rete, SERVFAIL) che non e' la risposta del dominio:
		// in nessuno dei due casi si accusa il dominio.
		return DNSResult{Status: store.DomainPendingDNS}
	}
	found := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(a); err == nil {
			found = append(found, ip.Unmap().WithZone(""))
		}
	}
	declared := map[netip.Addr]bool{}
	for _, s := range publicIPs {
		if ip, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil {
			declared[ip.Unmap()] = true
		}
	}
	if len(declared) > 0 && len(found) > 0 {
		all := true
		for _, ip := range found {
			if !declared[ip] {
				all = false
				break
			}
		}
		if all {
			return DNSResult{Status: store.DomainVerified}
		}
	}
	return DNSResult{Status: store.DomainError, Reason: c.mismatchReason(domain, cname, cnameErr, target, found, publicIPs)}
}

func (c DomainConfig) mismatchReason(domain, cname string, cnameErr error, target string, found []netip.Addr, publicIPs []string) string {
	strs := make([]string, len(found))
	for i, ip := range found {
		strs[i] = ip.String()
	}
	sort.Strings(strs)
	expected := "un CNAME verso " + target
	if len(publicIPs) > 0 {
		expected += " o i record A/AAAA verso " + strings.Join(publicIPs, ", ")
	}
	if cnameErr == nil && cname != "" && cname != domain {
		return fmt.Sprintf("il CNAME di %s punta a %s (risolve su %s); atteso %s", domain, cname, strings.Join(strs, ", "), expected)
	}
	policy := c.Policy
	if policy == nil {
		policy, _ = egress.NewPolicy(egress.Config{})
	}
	for _, ip := range found {
		if err := policy.CheckIP(ip); err != nil {
			return fmt.Sprintf("%s risolve su %s, un indirizzo riservato o interno, non quello dell'istanza; atteso %s", domain, strings.Join(strs, ", "), expected)
		}
	}
	return fmt.Sprintf("%s risolve su %s, che non è l'istanza; atteso %s", domain, strings.Join(strs, ", "), expected)
}

// ApplyDNSResult calcola il nuovo stato del dominio dopo un controllo, con la regola del periodo di
// grazia: un verified il cui DNS non coincide più resta verified (e con la rotta) finché non scade
// la grazia, poi passa in errore col motivo; un esito positivo la azzera. Un non-verified segue
// l'esito: verified, pending_dns o error.
func (c DomainConfig) ApplyDNSResult(d store.AppDomain, r DNSResult, now time.Time) store.AppDomainCheck {
	out := store.AppDomainCheck{Status: r.Status, CheckedAt: now, VerifiedAt: d.VerifiedAt}
	reason := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	switch {
	case r.Status == store.DomainVerified:
		if d.Status != store.DomainVerified || d.VerifiedAt == nil {
			t := now
			out.VerifiedAt = &t
		}
	case d.Status == store.DomainVerified:
		// Era verified e ora non coincide: grazia. Un esito «pending» (nessun record) conta come errore.
		since := now
		if d.FailingSince != nil {
			since = *d.FailingSince
		}
		why := r.Reason
		if why == "" {
			why = d.Normalized + " non risponde più con il record DNS atteso"
		}
		if now.Sub(since) < c.grace() {
			out.Status = store.DomainVerified
			out.FailingSince = &since
			out.StatusReason = nil
		} else {
			out.Status = store.DomainError
			out.FailingSince = &since
			out.StatusReason = reason(why)
		}
		return out
	case d.Status == store.DomainError && r.Status == store.DomainPendingDNS && d.FailingSince != nil:
		// Già in errore dopo la grazia e il DNS non risponde ancora: resta in errore, col motivo di prima.
		out.Status = store.DomainError
		out.FailingSince = d.FailingSince
		out.StatusReason = d.StatusReason
		return out
	}
	out.StatusReason = reason(r.Reason)
	return out
}

// DomainCheckDue dice se il dominio va ricontrollato ora (ritmo descritto in cima al file).
func DomainCheckDue(d store.AppDomain, now time.Time) bool {
	if d.LastChecked == nil {
		return true
	}
	every := domainVerifiedEvery
	if d.Status == store.DomainVerified && d.FailingSince != nil {
		every = domainFirstEvery // in grazia: la scadenza va colta presto
	} else if d.Status != store.DomainVerified {
		switch age := now.Sub(d.CreatedAt); {
		case age < time.Hour:
			every = domainFirstEvery
		case age < 24*time.Hour:
			every = domainSecondEvery
		default:
			every = domainLateEvery
		}
	}
	return now.Sub(*d.LastChecked) >= every
}
