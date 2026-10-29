package apps

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Let's Encrypt per i domini propri delle app pubbliche (v3-c, GIT-459, ADR 0002).
//
// Una sola configurazione ACME: il resolver `certificatesResolvers.letsencrypt` di Traefik, scritto
// dall'installer (deploy/install.sh) per `--tls letsencrypt` e per le app pubbliche. Core non ne ha
// una seconda: sa solo come si chiama il resolver (ACMEConfig.Resolver), lo cita nella rotta dei domini
// e osserva il risultato. Il certificato lo emette e lo rinnova Traefik (archivio acme.json nel
// namespace di Traefik); il Secret TLS non entra mai nel namespace dell'app.

// Nome del secondo oggetto IngressRoute di un'app: la rotta dei domini propri col resolver ACME. Resta
// distinta da quella dell'indirizzo automatico, che usa il wildcard della CA interna: un resolver sulla
// rotta dell'indirizzo automatico chiederebbe un certificato pubblico a Let's Encrypt per un nome che
// non è raggiungibile da internet.
const DomainsRouteName = WorkloadName + "-domains"

const (
	// acmeIssueTimeout: oltre questo tempo senza certificato un dominio verified passa in `error`.
	acmeIssueTimeout = 15 * time.Minute
	// certProbeEvery: ogni quanto si osserva il certificato servito da Traefik.
	certProbeEvery = time.Minute
	// port80Every: ogni quanto si ricontrolla la porta 80.
	port80Every = 10 * time.Minute
	port80Dial  = 5 * time.Second
)

// ACMEConfig è la parte ACME della configurazione, tutta dall'installazione.
type ACMEConfig struct {
	// Resolver: nome del certificatesResolver di Traefik. Vuoto = Let's Encrypt spento: le rotte dei
	// domini propri restano senza resolver e Traefik serve il certificato di default.
	Resolver string
	// Email dell'account ACME, quella dell'installazione (--tls-email). Solo lettura per core.
	Email string
	// TLSAddr: host:porta di Traefik (entrypoint websecure) raggiungibile da core, dove si legge il
	// certificato servito per ogni dominio (SNI). Vuoto = lo stato del certificato non si osserva.
	TLSAddr string
	// Port80: porta da controllare sugli IP pubblici dichiarati (default 80; i test usano un'altra).
	Port80 int
	// Seam dei test.
	Probe      func(ctx context.Context, addr, domain string) (*x509.Certificate, error)
	DialPort80 func(ctx context.Context, addr string) error
}

// Enabled: c'è un resolver e un indirizzo da cui osservare i certificati.
func (a ACMEConfig) Enabled() bool { return a.Resolver != "" }

func (a ACMEConfig) port80() int {
	if a.Port80 > 0 {
		return a.Port80
	}
	return 80
}

// Port80Status è l'esito dell'ultimo controllo della porta 80. Reachable nil = non verificato (nessun
// IP pubblico dichiarato o controllo non ancora eseguito).
type Port80Status struct {
	Reachable *bool
	CheckedAt *time.Time
	Error     string
}

type acmeState struct {
	mu        sync.Mutex
	issuing   map[uuid.UUID]time.Time // da quando un dominio attende il certificato (in memoria: dopo un riavvio il tempo riparte)
	port80    Port80Status
	port80Key string
	last80    time.Time
}

// ACMEStatus dà la configurazione ACME e l'ultimo esito sulla porta 80, per le impostazioni d'istanza.
func (c *Controller) ACMEStatus() (ACMEConfig, Port80Status) {
	c.acme.mu.Lock()
	defer c.acme.mu.Unlock()
	return c.cfg.ACME, c.acme.port80
}

// CertificateFromServed dà lo stato del certificato osservato su un dominio: valid se il certificato
// servito copre il dominio ed è in corso di validità. Il certificato di default di Traefik e il
// wildcard della CA interna non coprono il dominio, quindi non valgono.
func CertificateFromServed(cert *x509.Certificate, domain string, now time.Time) (valid bool, reason string) {
	if cert == nil {
		return false, "nessun certificato servito"
	}
	if err := cert.VerifyHostname(domain); err != nil {
		return false, "il certificato servito non copre " + domain + " (Traefik serve ancora quello di default)"
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return false, "il certificato servito è scaduto o non ancora valido"
	}
	return true, ""
}

// CheckCertificates osserva il certificato servito per ogni dominio verified col modo letsencrypt e ne
// scrive lo stato: none (DNS non verified), issuing (Traefik non ha ancora il certificato), valid o
// error (nessun certificato dopo acmeIssueTimeout). Il rinnovo lo fa Traefik: qui si vede la nuova
// scadenza. Con il resolver spento non fa niente.
func (c *Controller) CheckCertificates(ctx context.Context) {
	if c.st == nil || !c.cfg.ACME.Enabled() || c.cfg.ACME.TLSAddr == "" {
		return
	}
	on, err := c.publicAppsEnabled(ctx)
	if err != nil || !on {
		return
	}
	all, err := c.st.ListAppDomainsForCheck(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.cfg.Log.Warn("apps: lettura dei domini non riuscita", "err", err)
		}
		return
	}
	for _, d := range all {
		if ctx.Err() != nil {
			return
		}
		if d.CertMode != "letsencrypt" {
			continue
		}
		want, ok := c.certOutcome(ctx, d, c.cfg.Now())
		if !ok {
			continue
		}
		if sameCert(d, want) && !certDue(d, want, c.cfg.Now()) {
			continue
		}
		if _, err := c.st.SaveAppDomainCert(ctx, d.ID, want); err != nil && ctx.Err() == nil {
			c.cfg.Log.Warn("apps: stato del certificato non scritto", "domain", d.Normalized, "err", err)
		}
	}
}

func certDue(d store.AppDomain, want store.AppDomainCert, now time.Time) bool {
	return d.CertLastAttempt == nil || now.Sub(*d.CertLastAttempt) >= 10*time.Minute
}

func sameCert(d store.AppDomain, w store.AppDomainCert) bool {
	eq := func(a, b *time.Time) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Equal(*b)
	}
	str := func(a, b *string) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	return d.CertState == w.State && eq(d.CertValidFrom, w.ValidFrom) && eq(d.CertValidUntil, w.ValidUntil) && str(d.CertIssuer, w.Issuer) && str(d.CertError, w.Error)
}

// certOutcome calcola lo stato voluto del certificato di un dominio. ok=false: niente da scrivere.
func (c *Controller) certOutcome(ctx context.Context, d store.AppDomain, now time.Time) (store.AppDomainCert, bool) {
	out := store.AppDomainCert{LastAttempt: now}
	if d.Status != store.DomainVerified {
		c.acme.mu.Lock()
		delete(c.acme.issuing, d.ID)
		c.acme.mu.Unlock()
		if d.CertState == "none" && d.CertValidUntil == nil && d.CertError == nil {
			return out, false
		}
		out.State = "none"
		return out, true
	}
	probe := c.cfg.ACME.Probe
	if probe == nil {
		probe = ProbeServedCertificate
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cert, err := probe(pctx, c.cfg.ACME.TLSAddr, d.Normalized)
	if ok, _ := CertificateFromServed(cert, d.Normalized, now); err == nil && ok {
		c.acme.mu.Lock()
		delete(c.acme.issuing, d.ID)
		c.acme.mu.Unlock()
		from, until, issuer := cert.NotBefore.UTC(), cert.NotAfter.UTC(), cert.Issuer.CommonName
		if issuer == "" && len(cert.Issuer.Organization) > 0 {
			issuer = cert.Issuer.Organization[0]
		}
		out.State, out.ValidFrom, out.ValidUntil, out.Issuer = "valid", &from, &until, &issuer
		return out, true
	}
	// Un certificato già valido non torna a issuing per un errore di rete di un singolo controllo; se
	// Traefik serve ora un certificato che non copre il dominio, vale come nuova emissione.
	if err != nil && d.CertState == "valid" {
		return out, false
	}
	c.acme.mu.Lock()
	since, seen := c.acme.issuing[d.ID]
	if !seen {
		since = now
		c.acme.issuing[d.ID] = since
	}
	c.acme.mu.Unlock()
	if now.Sub(since) >= acmeIssueTimeout {
		msg := fmt.Sprintf("Traefik non ha ottenuto il certificato entro %d minuti: controlla che la porta 80 sia raggiungibile da internet e che il DNS punti all'istanza", int(acmeIssueTimeout/time.Minute))
		out.State, out.Error = "error", &msg
		return out, true
	}
	out.State = "issuing"
	return out, true
}

// ProbeServedCertificate legge il certificato che Traefik serve all'indirizzo addr per il nome domain
// (SNI). Non verifica la catena: serve a sapere cosa è servito, non a fidarsene.
func ProbeServedCertificate(ctx context.Context, addr, domain string) (*x509.Certificate, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{
		ServerName: domain, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, //nolint:gosec // si legge il certificato servito, la fiducia non è in gioco
	}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return nil, errors.New("connessione non TLS")
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("nessun certificato servito")
	}
	return certs[0], nil
}

// CheckPort80 prova ad aprire la porta 80 su ogni IP pubblico dichiarato. È una prova dall'interno
// dell'istanza: un firewall o un NAT senza hairpin possono dare un esito diverso da internet, e il
// messaggio d'errore lo dice. Senza IP dichiarati lo stato resta «non verificato».
func (c *Controller) CheckPort80(ctx context.Context) {
	if c.st == nil || !c.cfg.ACME.Enabled() {
		return
	}
	ips, err := c.st.PublicIPs(ctx)
	if err != nil {
		return
	}
	key := fmt.Sprint(ips)
	c.acme.mu.Lock()
	due := c.acme.port80Key != key || c.acme.last80.IsZero() || c.cfg.Now().Sub(c.acme.last80) >= port80Every
	c.acme.mu.Unlock()
	if !due {
		return
	}
	st := c.probePort80(ctx, ips)
	c.acme.mu.Lock()
	c.acme.port80, c.acme.port80Key, c.acme.last80 = st, key, c.cfg.Now()
	c.acme.mu.Unlock()
}

func (c *Controller) probePort80(ctx context.Context, ips []string) Port80Status {
	if len(ips) == 0 {
		return Port80Status{}
	}
	dial := c.cfg.ACME.DialPort80
	if dial == nil {
		dial = func(ctx context.Context, addr string) error {
			d := net.Dialer{Timeout: port80Dial}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		}
	}
	var failed []string
	for _, ip := range ips {
		if err := dial(ctx, net.JoinHostPort(ip, strconv.Itoa(c.cfg.ACME.port80()))); err != nil {
			failed = append(failed, ip)
		}
	}
	now := c.cfg.Now()
	ok := len(failed) == 0
	st := Port80Status{Reachable: &ok, CheckedAt: &now}
	if !ok {
		st.Error = fmt.Sprintf("la porta %d non risponde su %v (prova fatta dall'istanza: verifica firewall, NAT e inoltro)", c.cfg.ACME.port80(), failed)
	}
	return st
}
