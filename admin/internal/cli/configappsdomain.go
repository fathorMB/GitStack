package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// runConfigSetAppsDomain cambia il dominio delle app (H2): ogni app ha
// l'indirizzo <app>-<repo>-<owner>.<dominio>, coperto da un certificato wildcard
// *.<dominio> della CA interna. Serve anche a un'installazione v2 aggiornata,
// che non ha ancora un dominio.
func runConfigSetAppsDomain(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("config set apps-domain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			a.errorf("config set apps-domain: %v", err)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 {
		a.errorf("config set apps-domain: serve un solo dominio: gitstack config set apps-domain apps.azienda.it")
		return ExitUsage
	}
	domain := strings.ToLower(pos[0])
	if err := config.ValidateAppsDomain(domain); err != nil {
		a.errorf("config set apps-domain: %q non è un dominio valido: %v", domain, err)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	code = a.applyAppsDomain(ctx, cfg, path, domain, "config set apps-domain")
	if code != ExitOK || !cfg.AppsEnabled || cfg.AppsDomain == domain {
		return code
	}
	// Hosting acceso: core riceve il dominio nuovo (GITSTACK_CORE_APPS_DOMAIN).
	if c := a.helmApps(ctx, cfg, "config set apps-domain", "apps.domain="+domain); c != ExitOK {
		return c
	}
	return ExitOK
}

// applyAppsDomain imposta il dominio delle app: controlli, certificato wildcard e config.yaml.
// Non tocca il chart (lo fa chi chiama, se l'hosting è acceso). Dopo, cfg.AppsDomain è il dominio nuovo
// solo se il chiamante lo aggiorna: qui resta il valore di prima, perché serve a capire se è cambiato.
func (a *App) applyAppsDomain(ctx context.Context, cfg *config.Config, path, domain, cmd string) int {
	if domain == strings.ToLower(cfg.Host) {
		a.errorf(cmd+": %q coincide con l'host di GitStack: scegli un dominio distinto (consigliato) o un suo sottodominio, per esempio apps.%s.", domain, cfg.Host)
		return ExitUsage
	}
	if cfg.AppsDomain == domain {
		_, _ = fmt.Fprintf(a.Stdout, "Il dominio delle app è già %s: niente da fare.\n", domain)
		return ExitOK
	}
	if strings.HasSuffix(strings.ToLower(cfg.Host), "."+domain) {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: l'host di GitStack (%s) sta dentro %s: il wildcard copre anche il suo nome, e un'app chiamata come il primo pezzo dell'host lo oscurerebbe. Meglio un dominio separato.\n", cfg.Host, domain)
	}
	if strings.HasSuffix(domain, ".local") {
		_, _ = fmt.Fprintln(a.Stderr, "ATTENZIONE: un dominio .local usa mDNS, che non ha record wildcard: serve un DNS vero (router, dnsmasq, DNS aziendale).")
	}
	if w := a.checkHostResolves(ctx, "gitstack-dns-check."+domain); w != "" {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: il record DNS wildcard *.%s non è ancora a posto: %s Crealo verso l'IP di GitStack prima che gli utenti aprano le app (deploy/README.md, «Dominio delle app»).\n", domain, w)
	}

	tlsDir := a.Getenv("GITSTACK_TLS_DIR")
	if tlsDir == "" {
		tlsDir = filepath.Join(filepath.Dir(path), "tls")
	}
	tlsBin := a.Getenv("GITSTACK_TLS_BIN")
	if tlsBin == "" {
		tlsBin = tlsToolPath
	}

	// 1. Certificato wildcard (CA interna) o controllo dei SAN (certificato del cliente).
	switch cfg.TLS {
	case "internal", "custom":
		_, _ = fmt.Fprintf(a.Stdout, "==> Rigenero il certificato (SAN wildcard: *.%s)\n", domain)
		out, err := a.Runner.Run(ctx, []string{"GITSTACK_TLS_DIR=" + tlsDir}, tlsBin, "ensure", "--apps-domain", domain)
		if err != nil {
			a.errorf(cmd+": gitstack-tls: %v\n%s", err, strings.TrimSpace(string(out)))
			return ExitUnexpected
		}
		if len(out) > 0 {
			_, _ = a.Stdout.Write(out)
		}
	default:
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: con tls: %q il certificato wildcard *.%s non viene emesso dalla CA interna: l'indirizzo delle app è registrato, il certificato arriva con le app pubbliche (Let's Encrypt per dominio).\n", cfg.TLS, domain)
	}

	// 2. Configurazione, per ultima: se il passo prima fallisce si può rilanciare.
	if err := config.SetFields(path, map[string]string{"apps_domain": domain}); err != nil {
		a.errorf(cmd+": %v", err)
		return ExitUnexpected
	}
	updateInstallConfKey(tlsDir, "APPS_DOMAIN", domain)

	_, _ = fmt.Fprintf(a.Stdout, "Dominio delle app impostato a %s (config: %s).\n", domain, path)
	_, _ = fmt.Fprintf(a.Stdout, "Gli indirizzi automatici delle app sono <app>-<repo>-<owner>.%s. Le app già create mantengono il loro indirizzo finché non vengono aggiornate.\n", domain)
	return ExitOK
}

// updateInstallConfKey imposta KEY=valore in install.conf (aggiungendo la riga
// se manca), perché rieseguire install.sh senza --apps-domain non riporti il
// dominio vecchio. Best effort.
func updateInstallConfKey(tlsDir, key, value string) {
	p := filepath.Join(tlsDir, "install.conf")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	done := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i] = key + "=" + value
			done = true
		}
	}
	if !done {
		lines = append(lines, key+"="+value)
	}
	_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
