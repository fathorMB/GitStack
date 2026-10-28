package apps

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Ciclo del controllo periodico dei domini propri (v3-c, GIT-458) e rotta degli host attivi.

// domainConfig è la configurazione del controllo: quella del Config con il dominio delle app.
func (c *Controller) domainConfig() DomainConfig {
	d := c.cfg.Domains
	if d.AppsDomain == "" {
		d.AppsDomain = c.cfg.AppsDomain
	}
	return d
}

// CheckDomains ricontrolla il DNS dei domini che ne hanno bisogno (DomainCheckDue), salva lo stato e,
// se l'insieme dei domini attivi di un'app è cambiato, riallinea la sua rotta. Un errore su un dominio
// non ferma gli altri. Con le app pubbliche spente il controllo è sospeso (i domini si conservano).
func (c *Controller) CheckDomains(ctx context.Context) {
	if c.st == nil {
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
	now := time.Now()
	ips, _ := c.st.PublicIPs(ctx)
	for _, d := range all {
		if ctx.Err() != nil {
			return
		}
		if !DomainCheckDue(d, now) {
			continue
		}
		app, err := c.st.GetAppByID(ctx, d.AppID)
		if err != nil {
			continue
		}
		if _, changed, err := RecheckDomain(ctx, c.st, c.domainConfig(), app, d, ips, c.cfg.Log); err != nil {
			c.cfg.Log.Warn("apps: controllo del dominio non riuscito", "domain", d.Normalized, "err", err)
		} else if changed {
			c.RefreshRoute(ctx, app)
		}
	}
}

// RecheckDomain fa il controllo del DNS di un dominio e ne salva l'esito. Ritorna il dominio
// aggiornato e se il suo contributo alla rotta e' cambiato (entrata o uscita da verified). Lo usano il
// ciclo periodico e POST .../recheck.
func RecheckDomain(ctx context.Context, st *store.Store, dc DomainConfig, app store.App, d store.AppDomain, publicIPs []string, logs ...*slog.Logger) (store.AppDomain, bool, error) {
	res := dc.CheckDNS(ctx, d.Normalized, app.AddressLabel, publicIPs)
	if res.ResolverErr != nil && len(logs) > 0 && logs[0] != nil {
		logs[0].Warn("apps: errore del resolver durante il controllo DNS", "domain", d.Normalized, "err", res.ResolverErr)
	}
	upd, err := st.SaveAppDomainCheck(ctx, d.ID, dc.ApplyDNSResult(d, res, time.Now()))
	if err != nil {
		return d, false, err
	}
	return upd, (d.Status == store.DomainVerified) != (upd.Status == store.DomainVerified), nil
}

// RefreshRoute riallinea subito la rotta dell'app (un dominio tolto o cambiato di stato non aspetta il
// giro di sync). Gli errori finiscono nel log: il giro di sync riprova.
func (c *Controller) RefreshRoute(ctx context.Context, app store.App) {
	if err := c.ensureRoute(ctx, app, app.Config); err != nil {
		c.cfg.Log.Warn("apps: rotta non riallineata", "app", app.ID, "err", err)
	}
}

// domainHosts sono gli host dei domini propri da aggiungere alla rotta dell'app: solo i domini con
// DNS verified (anche in periodo di grazia), e solo per un'app pubblica. Chiamata solo da ensureRoute,
// dopo il controllo dell'interruttore d'istanza.
//
// Ritorna due insiemi: own, gli host che restano nella rotta dell'indirizzo automatico (certificato
// caricato dal cliente, o tutti se Let's Encrypt è spento), e acme, gli host con certificato Let's
// Encrypt, che vanno nella rotta col resolver (BuildDomainsRoute). Senza resolver acme è vuoto.
func (c *Controller) domainHosts(ctx context.Context, app store.App) (own, acme []string, err error) {
	if app.Access != "public" {
		return nil, nil, nil
	}
	if !c.cfg.ACME.Enabled() {
		if c.domainsOf != nil {
			own, err = c.domainsOf(ctx, app)
		} else if c.st != nil {
			own, err = c.st.VerifiedDomainHosts(ctx, app.ID)
		}
		return own, nil, err
	}
	if c.domainsOf != nil {
		acme, err = c.domainsOf(ctx, app)
		return nil, acme, err
	}
	if c.st == nil {
		return nil, nil, nil
	}
	if acme, err = c.st.VerifiedDomainHostsMode(ctx, app.ID, "letsencrypt"); err != nil {
		return nil, nil, err
	}
	own, err = c.st.VerifiedDomainHostsMode(ctx, app.ID, "custom")
	return own, acme, err
}

// routeMatch è la regola Traefik della rotta: l'indirizzo automatico e, in ordine, i domini propri attivi.
func routeMatch(host string, extra []string) string {
	parts := []string{"Host(`" + host + "`)"}
	for _, h := range extra {
		parts = append(parts, "Host(`"+h+"`)")
	}
	return strings.Join(parts, " || ")
}
