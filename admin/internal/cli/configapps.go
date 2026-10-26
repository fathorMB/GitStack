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

// Hosting delle app (GIT-375, ADR 0002): `gitstack config set apps on|off` e le opzioni
// --enable-apps / --apps-domain di `gitstack upgrade` accendono il controller delle app in core, i
// suoi permessi Kubernetes minimi e il forward-auth del gateway (chart: apps.enabled, apps.domain;
// l'indirizzo del forward-auth e le chiavi di firma li porta il chart da solo, GIT-347).

// defaultAppsDomain è il dominio che install.sh sceglie quando non ne viene indicato uno.
func defaultAppsDomain(host string) string {
	if isIPv4(host) || host == "localhost" {
		return "apps.gitstack.internal"
	}
	return "apps." + host
}

// chartHasApps dice se il chart sul disco conosce l'hosting delle app (values.yaml con `apps:`):
// una v2 non aggiornata no, e `helm upgrade --reuse-values` con le sue chiavi non avrebbe effetto.
func chartHasApps(chartDir string) bool {
	b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "apps:") {
			return true
		}
	}
	return false
}

// tlsDirFor è la cartella dei file TLS e di install.conf (accanto a config.yaml).
func (a *App) tlsDirFor(configPath string) string {
	if d := a.Getenv("GITSTACK_TLS_DIR"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(configPath), "tls")
}

// helmApps applica valori del chart (--reuse-values) alla release e aspetta che sia sana.
func (a *App) helmApps(ctx context.Context, cfg *config.Config, cmd string, sets ...string) int {
	if cfg.ChartDir == "" || !dirHasChart(cfg.ChartDir) {
		a.errorf("%s: il chart di GitStack non è sul disco (chart_dir %q): rilancia deploy/install.sh, che lo copia.", cmd, cfg.ChartDir)
		return ExitConfig
	}
	if !chartHasApps(cfg.ChartDir) {
		a.errorf("%s: il chart in %s non conosce l'hosting delle app (installazione precedente): aggiorna prima con «gitstack upgrade --enable-apps».", cmd, cfg.ChartDir)
		return ExitConfig
	}
	args := []string{"upgrade", cfg.Release, cfg.ChartDir, "--namespace", cfg.Namespace, "--reuse-values"}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	args = append(args, "--wait", "--timeout", "5m")
	_, _ = fmt.Fprintf(a.Stdout, "==> Aggiorno i servizi (%s)\n", strings.Join(sets, ", "))
	if out, err := a.Runner.Run(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, a.helmBin(), args...); err != nil {
		a.errorf("%s: helm upgrade fallito: %v\n%s", cmd, err, strings.TrimSpace(string(out)))
		return ExitCluster
	}
	return ExitOK
}

// chooseAppsDomain sceglie il dominio quando si accende l'hosting: --apps-domain, altrimenti
// quello già in config.yaml, altrimenti (installazione v2 senza dominio) il predefinito con avviso.
// Il dominio non valido è un errore d'uso (ok=false).
func (a *App) chooseAppsDomain(cfg *config.Config, flagValue, cmd string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(flagValue))
	if d == "" {
		d = cfg.AppsDomain
	}
	if d == "" {
		d = defaultAppsDomain(strings.ToLower(cfg.Host))
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: --apps-domain non indicato e l'installazione non ha un dominio delle app: uso il predefinito %s. Crea il record DNS wildcard *.%s verso questa macchina, oppure scegli un dominio con --apps-domain (o, dopo, «gitstack config set apps-domain <dominio>»). Consigliato un dominio distinto da quello di GitStack.\n", d, d)
	}
	if err := config.ValidateAppsDomain(d); err != nil {
		a.errorf("%s: dominio delle app %q non valido: %v", cmd, d, err)
		return "", false
	}
	return d, true
}

// runConfigSetApps implementa `gitstack config set apps on|off`.
func runConfigSetApps(ctx context.Context, a *App, args []string) int {
	const cmd = "config set apps"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	domainFlag := fs.String("apps-domain", "", "dominio delle app (default: quello già impostato, altrimenti apps.<host> con avviso)")
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			a.errorf("%s: %v", cmd, err)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
		a.errorf("%s: serve on oppure off: gitstack config set apps on [--apps-domain apps.azienda.it]", cmd)
		return ExitUsage
	}
	on := pos[0] == "on"
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}

	tlsDir := a.tlsDirFor(path)

	if !on {
		if !cfg.AppsEnabled {
			_, _ = fmt.Fprintln(a.Stdout, "L'hosting delle app è già spento: niente da fare.")
			return ExitOK
		}
		if c := a.helmApps(ctx, cfg, cmd, "apps.enabled=false"); c != ExitOK {
			return c
		}
		if err := config.SetFields(path, map[string]string{"apps_enabled": "false"}); err != nil {
			a.errorf("%s: %v", cmd, err)
			return ExitUnexpected
		}
		updateInstallConfKey(tlsDir, "APPS_ENABLED", "false")
		_, _ = fmt.Fprintln(a.Stdout, "Hosting delle app spento. Le app già create restano nel cluster e nel database ma core non le gestisce più; l'API delle app risponde 503 finché non lo riaccendi.")
		return ExitOK
	}

	domain, ok := a.chooseAppsDomain(cfg, *domainFlag, cmd)
	if !ok {
		return ExitUsage
	}
	if domain != cfg.AppsDomain {
		if c := a.applyAppsDomain(ctx, cfg, path, domain, cmd); c != ExitOK {
			return c
		}
	}
	sets := []string{"apps.enabled=true", "apps.domain=" + domain}
	if c := a.helmApps(ctx, cfg, cmd, sets...); c != ExitOK {
		_, _ = fmt.Fprintln(a.Stderr, "Il dominio è già stato impostato: dopo aver sistemato la causa rilancia lo stesso comando.")
		return c
	}
	if err := config.SetFields(path, map[string]string{"apps_enabled": "true"}); err != nil {
		a.errorf("%s: %v", cmd, err)
		return ExitUnexpected
	}
	updateInstallConfKey(tlsDir, "APPS_ENABLED", "true")
	_, _ = fmt.Fprintf(a.Stdout, "Hosting delle app acceso (dominio %s).\n", domain)
	return ExitOK
}
