package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/dbpki"
	"github.com/fathorMB/GitStack/admin/internal/sysinfo"
)

// Database gestiti (v3-d, ADR 0003, GIT-557): `gitstack config set databases on|off` e le opzioni
// --enable-databases / --databases-port-range di `gitstack upgrade` accendono il controller dei database in
// core (chart: databases.enabled, databases.lan.portRange). Spenti di default (DB23).
//
// Spegnere NON cancella niente: i database (StatefulSet e volumi nei namespace gitstack-db-*), i loro dump
// (volume databases-data, resource-policy keep), la CA intermedia e la chiave di dump restano. core smette di
// gestirli e l'API risponde 422 databases_disabled finché non si riaccende. App e repo non si toccano: si
// cambiano solo le chiavi databases.* del chart.

// defaultDatabasesPortRange è l'intervallo del chart (values.yaml databases.lan.portRange).
const defaultDatabasesPortRange = "15432-15531"

// chartHasDatabases dice se il chart sul disco conosce i database gestiti (values.yaml con `databases:`).
func chartHasDatabases(chartDir string) bool {
	b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "databases:") {
			return true
		}
	}
	return false
}

// databasesSets sono i valori del chart per accendere o spegnere i database. L'intervallo delle porte (0 = non
// cambiarlo) vale solo da accesi.
func databasesSets(on bool, from, to int) []string {
	sets := []string{"databases.enabled=" + strconv.FormatBool(on)}
	if on && from > 0 && to > 0 {
		sets = append(sets, "databases.lan.portRange.from="+strconv.Itoa(from), "databases.lan.portRange.to="+strconv.Itoa(to))
	}
	return sets
}

// databasesConfigFields sono le chiavi di config.yaml che seguono la scelta.
func databasesConfigFields(on bool, from, to int) map[string]string {
	f := map[string]string{"databases_enabled": strconv.FormatBool(on)}
	if on && from > 0 && to > 0 {
		f["databases_port_from"] = strconv.Itoa(from)
		f["databases_port_to"] = strconv.Itoa(to)
	}
	return f
}

// portRangeOf è l'intervallo da usare accendendo: quello chiesto, altrimenti quello già salvato, altrimenti 0-0
// (default del chart).
func portRangeOf(cfg *config.Config, flagValue, cmd string, a *App) (from, to int, ok bool) {
	if flagValue != "" {
		f, t, err := config.ParseDatabasesPortRange(flagValue)
		if err != nil {
			a.errorf("%s: --databases-port-range %q: %v", cmd, flagValue, err)
			return 0, 0, false
		}
		return f, t, true
	}
	return cfg.DatabasesPortFrom, cfg.DatabasesPortTo, true
}

// databasesPreflight confronta l'host con il profilo dei database (RAM e disco libero). Ritorna false se
// l'accensione va fermata; gli avvisi sul consigliato vanno su stderr. skip = --skip-preflight.
func (a *App) databasesPreflight(cmd string, skip bool) bool {
	if skip {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: %s: --skip-preflight, nessun controllo delle risorse per i database gestiti.\n", cmd)
		return true
	}
	if a.NodeProbe == nil {
		return true
	}
	fails, warns := sysinfo.DatabasesPreflight(a.NodeProbe())
	for _, w := range warns {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: %s\n", w)
	}
	if len(fails) > 0 {
		for _, f := range fails {
			a.errorf("%s: %s", cmd, f)
		}
		_, _ = fmt.Fprintln(a.Stderr, "Niente è stato modificato. Libera risorse, oppure --skip-preflight a tuo rischio.")
		return false
	}
	return true
}

// ensureDatabaseSecrets crea la CA intermedia e la chiave di dump se mancano (prima del chart: core legge la
// chiave di dump già al primo avvio). Senza la radice sull'host (certificato del cliente) l'intermedia non si
// può creare: avvisa e prosegue, come l'upgrade.
func (a *App) ensureDatabaseSecrets(ctx context.Context, cfg *config.Config, path, cmd string) int {
	cl, err := a.cluster(cfg)
	if err != nil {
		a.errorf("%s: %v", cmd, err)
		return ExitCluster
	}
	res, err := dbpki.Ensure(ctx, dbpki.Options{Cluster: cl, Release: cfg.Release, TLSDir: a.tlsDirFor(path)})
	if res.CreatedDumpKey {
		_, _ = fmt.Fprintf(a.Stdout, "Chiave di dump dei database creata (%s).\n", dbpki.DumpKeySecretName(cfg.Release))
	}
	if res.CreatedCA {
		_, _ = fmt.Fprintf(a.Stdout, "CA intermedia dei database creata (%s).\n", dbpki.CASecretName(cfg.Release))
	}
	switch {
	case errors.Is(err, dbpki.ErrNoRootCA):
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: %s: CA intermedia dei database NON creata, la radice dell'istanza non è sull'host (%v). I database con TLS non partiranno finché non c'è.\n", cmd, err)
	case err != nil:
		a.errorf("%s: segreti dei database gestiti: %v", cmd, err)
		return ExitUnexpected
	}
	return ExitOK
}

// databasesFirewallHint ricorda le porte da aprire nel firewall (gitstack non modifica mai ufw).
func (a *App) databasesFirewallHint(from, to int) {
	rng := defaultDatabasesPortRange
	if from > 0 && to > 0 {
		rng = fmt.Sprintf("%d-%d", from, to)
	}
	_, _ = fmt.Fprintf(a.Stdout, "L'accesso dalla LAN di ogni database è spento finché non lo accende il portale; usa una porta TCP tra %s. Se usi ufw, aprile solo alla tua rete (GitStack non lo modifica): ufw allow from <rete> to any port %s proto tcp\n",
		rng, strings.Replace(rng, "-", ":", 1))
}

// runConfigSetDatabases implementa `gitstack config set databases on|off`.
func runConfigSetDatabases(ctx context.Context, a *App, args []string) int {
	const cmd = "config set databases"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	portRange := fs.String("databases-port-range", "", "intervallo DA-A delle porte TCP per l'accesso LAN dei database (default: quello già impostato, altrimenti "+defaultDatabasesPortRange+")")
	skip := fs.Bool("skip-preflight", false, "non controllare RAM e disco libero (a tuo rischio)")
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
		a.errorf("%s: serve on oppure off: gitstack config set databases on [--databases-port-range 15432-15531]", cmd)
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
		if *portRange != "" {
			a.errorf("%s: --databases-port-range vale solo con on", cmd)
			return ExitUsage
		}
		if !cfg.DatabasesEnabled {
			_, _ = fmt.Fprintln(a.Stdout, "I database gestiti sono già spenti: niente da fare.")
			return ExitOK
		}
		if cfg.ChartDir != "" && dirHasChart(cfg.ChartDir) && !chartHasDatabases(cfg.ChartDir) {
			a.errorf("%s: il chart in %s non conosce i database gestiti (installazione precedente): aggiorna prima con «gitstack upgrade --enable-databases».", cmd, cfg.ChartDir)
			return ExitConfig
		}
		// Solo le chiavi databases.* del chart: app, repo e Postgres di GitStack non si toccano.
		if c := a.helmSets(ctx, cfg, cmd, databasesSets(false, 0, 0)...); c != ExitOK {
			return c
		}
		if err := config.SetFields(path, databasesConfigFields(false, 0, 0)); err != nil {
			a.errorf("%s: %v", cmd, err)
			return ExitUnexpected
		}
		updateInstallConfKey(tlsDir, "DATABASES_ENABLED", "false")
		_, _ = fmt.Fprintln(a.Stdout, "Database gestiti spenti. Nessun database e nessun dato è stato cancellato: i database già creati continuano a girare nel cluster, i loro dump e le chiavi restano, ma core non li gestisce più e l'API risponde 422 databases_disabled finché non li riaccendi con «gitstack config set databases on».")
		return ExitOK
	}

	from, to, ok := portRangeOf(cfg, *portRange, cmd, a)
	if !ok {
		return ExitUsage
	}
	if cfg.ChartDir != "" && dirHasChart(cfg.ChartDir) && !chartHasDatabases(cfg.ChartDir) {
		a.errorf("%s: il chart in %s non conosce i database gestiti (installazione precedente): aggiorna prima con «gitstack upgrade --enable-databases».", cmd, cfg.ChartDir)
		return ExitConfig
	}
	if !a.databasesPreflight(cmd, *skip) {
		return ExitRefused
	}
	if c := a.ensureDatabaseSecrets(ctx, cfg, path, cmd); c != ExitOK {
		return c
	}
	if c := a.helmSets(ctx, cfg, cmd, databasesSets(true, from, to)...); c != ExitOK {
		return c
	}
	if err := config.SetFields(path, databasesConfigFields(true, from, to)); err != nil {
		a.errorf("%s: %v", cmd, err)
		return ExitUnexpected
	}
	updateInstallConfKey(tlsDir, "DATABASES_ENABLED", "true")
	if from > 0 && to > 0 {
		updateInstallConfKey(tlsDir, "DATABASES_PORT_RANGE", fmt.Sprintf("%d-%d", from, to))
	}
	_, _ = fmt.Fprintln(a.Stdout, "Database gestiti accesi.")
	a.databasesFirewallHint(from, to)
	return ExitOK
}
