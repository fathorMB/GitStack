package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// Codici di uscita di `gitstack upgrade` (oltre a ExitRefused = 6).
const (
	// ExitRolledBack: l'aggiornamento è fallito ed è tornato com'era.
	ExitRolledBack = 7
	// ExitBroken: fallito e il rollback non è riuscito: serve un intervento.
	ExitBroken = 8
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runUpgrade(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	to := fs.String("to", "", "versione di destinazione: tag di versione, sha del commit o sha-<commit> (default: ultimo commit di "+upgrade.DefaultRef+")")
	version := fs.String("version", "", "versione di rilascio vX.Y.Z (anche -rc.N): equivale a --to, ma accetta solo tag SemVer")
	dest := fs.String("dest", "", "cartella del backup preventivo (default backup.destination del config)")
	keyFile := fs.String("key-file", "", "file con la chiave per cifrare il backup preventivo (AES-256-GCM)")
	timeout := fs.Duration("timeout", upgrade.DefaultTimeout, "tempo massimo per l'aggiornamento dei servizi e per la loro salute")
	dry := fs.Bool("dry-run", false, "esegue solo i controlli sulla destinazione, senza modificare nulla")
	k3sMinor := fs.Bool("k3s", false, "consente l'aggiornamento di k3s a una nuova minore (una alla volta); le patch si applicano da sole")
	enableApps := fs.Bool("enable-apps", false, "accende l'hosting delle app (chart apps.enabled): core riceve il controller delle app e i permessi Kubernetes minimi dell'ADR 0002")
	appsDomain := fs.String("apps-domain", "", "con --enable-apps: dominio delle app (default: quello già impostato, altrimenti apps.<host> con avviso)")
	enableDatabases := fs.Bool("enable-databases", false, "accende i database gestiti (chart databases.enabled, ADR 0003): core riceve il controller dei database PostgreSQL e i suoi permessi; richiede RAM e disco oltre il profilo minimo")
	dbPortRange := fs.String("databases-port-range", "", "con --enable-databases: intervallo DA-A delle porte TCP per l'accesso LAN dei database (default: quello già impostato, altrimenti "+defaultDatabasesPortRange+")")
	skipPreflight := fs.Bool("skip-preflight", false, "con --enable-databases: non controllare RAM e disco libero (a tuo rischio)")
	var sets, values multiFlag
	fs.Var(&sets, "set", "valore del chart (chiave=valore), ripetibile")
	fs.Var(&values, "values", "file di valori del chart, ripetibile")
	allowUnsigned := fs.Bool("allow-unsigned-mirrors", false, "eccezione N9: non verificare le firme cosign (mirror interno non firmato)")
	// Flag interni del re-exec (GIT-490): non sono per l'utente.
	reexecProtocol := fs.Bool("reexec-protocol", false, "")
	reexecReq := fs.String("reexec-request", "", "")
	reexecReply := fs.String("reexec-reply", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("upgrade: %v", err)
		a.usage(a.Stderr)
		return ExitUsage
	}
	if *reexecProtocol {
		_, _ = fmt.Fprintln(a.Stdout, upgrade.ReexecProtocol)
		return ExitOK
	}
	if *version != "" {
		if *to != "" {
			a.errorf("upgrade: --version e --to si escludono a vicenda")
			return ExitUsage
		}
		if !upgrade.IsVersionTag(*version) {
			a.errorf("upgrade: --version %q non valida: serve vX.Y.Z (o vX.Y.Z-rc.N); per uno sha usa --to", *version)
			return ExitUsage
		}
		*to = *version
	}
	if *timeout < time.Minute {
		a.errorf("upgrade: --timeout troppo breve (almeno 1m)")
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	if *appsDomain != "" && !*enableApps {
		a.errorf("upgrade: --apps-domain vale solo con --enable-apps")
		return ExitUsage
	}
	// Hosting delle app (GIT-375): dominio (anche per una v2 che non ne ha uno) e valori del chart.
	var appsSets []string
	var appsConfig map[string]string
	appsDomainNew := ""
	if *enableApps {
		d, ok := a.chooseAppsDomain(cfg, *appsDomain, "upgrade")
		if !ok {
			return ExitUsage
		}
		appsConfig = map[string]string{"apps_enabled": "true"}
		appsSets = []string{"apps.enabled=true", "apps.domain=" + d}
		if d != cfg.AppsDomain {
			appsDomainNew = d
		}
		if *dry {
			_, _ = fmt.Fprintf(a.Stdout, "==> --dry-run: l'hosting delle app si accenderebbe con il dominio %s.\n", d)
		}
	}
	// Database gestiti (GIT-557, DB23): valori del chart e config.yaml, con il preflight di risorse.
	var dbSets []string
	var dbConfig map[string]string
	dbFrom, dbTo := 0, 0
	if *dbPortRange != "" && !*enableDatabases {
		a.errorf("upgrade: --databases-port-range vale solo con --enable-databases")
		return ExitUsage
	}
	if *enableDatabases {
		var ok bool
		if dbFrom, dbTo, ok = portRangeOf(cfg, *dbPortRange, "upgrade", a); !ok {
			return ExitUsage
		}
		if cfg.ChartDir != "" && dirHasChart(cfg.ChartDir) && !chartHasDatabases(cfg.ChartDir) && *reexecReq == "" {
			_, _ = fmt.Fprintln(a.Stderr, "Il chart sul disco è precedente ai database gestiti: l'aggiornamento porta quello della destinazione.")
		}
		if *reexecReq == "" && !a.databasesPreflight("upgrade", *skipPreflight) {
			return ExitRefused
		}
		dbSets = databasesSets(true, dbFrom, dbTo)
		dbConfig = databasesConfigFields(true, dbFrom, dbTo)
		if *dry {
			_, _ = fmt.Fprintln(a.Stdout, "==> --dry-run: i database gestiti si accenderebbero.")
		}
	} else if *skipPreflight {
		a.errorf("upgrade: --skip-preflight vale solo con --enable-databases")
		return ExitUsage
	}
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = readKey(*keyFile); err != nil {
			a.errorf("upgrade: %v", err)
			return ExitUsage
		}
	} else if cfg.Backup.S3 != nil {
		// Con S3 configurato ogni archivio è cifrato, anche il backup
		// preventivo (che resta locale: non lo carico su S3).
		var err error
		if key, err = s3Key(cfg); err != nil {
			a.errorf("upgrade: backup.s3.key_file: %v", err)
			return ExitConfig
		}
	}
	cl, err := a.cluster(cfg)
	if err != nil {
		a.errorf("upgrade: %v", err)
		return ExitCluster
	}
	bo := a.options(cfg, path, key)
	bo.DestDir = *dest
	col := &status.Collector{Runner: a.Runner, HTTP: a.HTTP, LookPath: a.LookPath}
	configDir := a.configDir(path)
	o := &upgrade.Options{
		Cfg:             cfg,
		ConfigFile:      bo.ConfigFile,
		Cluster:         cl,
		Runner:          a.Runner,
		Health:          func(ctx context.Context) *status.Report { return col.Collect(ctx, cfg, configDir) },
		Backup:          *bo,
		To:              *to,
		DryRun:          *dry,
		AllowK3sMinor:   *k3sMinor,
		Timeout:         *timeout,
		Sets:            append(append(append([]string{}, sets...), appsSets...), dbSets...),
		ExtraConfig:     mergeConfigFields(appsConfig, dbConfig),
		ValueFiles:      values,
		HelmBin:         a.helmBin(),
		ExePath:         a.exePath(),
		Repo:            a.Getenv("GITSTACK_REPO"),
		TraefikManifest: a.Getenv("GITSTACK_TRAEFIK_MANIFEST"),
		TLSDir:          a.tlsDirFor(path),
		TLSToolPath:     a.tlsToolPath(),
		// N9a: k3s si aggiorna dal mirror dei download, se configurato, e il
		// proxy arriva dall'ambiente (exportProxyEnv in loadConfig).
		K3sDownloadMirror: cfg.Network.DownloadMirror,
		SystemdDir:        a.networkPaths().SystemdDir,
		Ref:               a.Getenv("GITSTACK_REF"),
		Log:               a.Stdout,
		// V5: firme cosign di binario, chart e immagini, col cosign pinnato
		// dall'installer. L'eccezione N9 è quella dell'installazione (config)
		// o, per questo solo aggiornamento, il flag. Mirror dei download e delle
		// immagini (N9a): anche ciò che ne arriva si verifica.
		DownloadMirror: cfg.Network.DownloadMirror,
		V5: upgrade.V5Config{
			Repo:             a.Getenv("GITSTACK_REPO"),
			CosignPath:       cfg.V5.CosignPath,
			AllowUnsigned:    cfg.V5.AllowUnsigned || *allowUnsigned,
			RegistryMirror:   cfg.Network.RegistryMirror,
			RegistryUsername: cfg.Network.RegistryUsername,
			RegistryPassword: cfg.Network.RegistryPassword,
		},
	}
	if o.V5.AllowUnsigned {
		_, _ = fmt.Fprintln(a.Stderr, "AVVISO:", status.SupplyChainUnsignedWarning)
	}
	if status.ServesCA(cfg) {
		o.CheckCA = func(ctx context.Context) error { return status.CheckCA(ctx, cfg, nil) }
	}
	if a.TuneUpgrade != nil {
		a.TuneUpgrade(o)
	}

	// Lato del processo nuovo: esegue il passo chiesto dal vecchio (GIT-490) e basta.
	if *reexecReq != "" {
		return a.runReexecChild(ctx, o, *reexecReq, *reexecReply)
	}
	o.Reexec = a.reexecFunc(path, *dest, *keyFile, *timeout)

	// GIT-559: il dominio nuovo (e il suo certificato wildcard) si applica DOPO upgrade.Run, quando
	// gitstack-tls è già quello della destinazione (un host con lo script precedente a GIT-346 non
	// conosce --apps-domain). Se l'upgrade non riesce il dominio non viene toccato e l'output dice
	// quale comando lanciare. Mai in --dry-run.
	pendingDomain := func() {
		if appsDomainNew != "" && !*dry {
			_, _ = fmt.Fprintf(a.Stderr, "Il dominio delle app NON è stato applicato (resta %q). Dopo aver sistemato la causa rilancia lo stesso `gitstack upgrade --enable-apps --apps-domain %s`, oppure applicalo da solo con `gitstack config set apps-domain %s`.\n", cfg.AppsDomain, appsDomainNew, appsDomainNew)
		}
	}

	res, err := upgrade.Run(ctx, o)
	var ref *upgrade.RefusedError
	switch {
	case errors.As(err, &ref):
		a.errorf("upgrade rifiutato: %v", err)
		_, _ = fmt.Fprintln(a.Stderr, "Niente è stato modificato.")
		pendingDomain()
		return ExitRefused
	case err != nil:
		a.errorf("upgrade: %v", err)
		for _, l := range res.Summary {
			_, _ = fmt.Fprintln(a.Stderr, l)
		}
		pendingDomain()
		return ExitUnexpected
	}
	if *enableApps && !*dry {
		switch res.Outcome {
		case upgrade.Upgraded:
			if c := a.applyAppsDomainAfterRun(ctx, cfg, path, appsDomainNew, res); c != ExitOK {
				return c
			}
			// config.yaml l'ha già scritto l'upgrade (ExtraConfig), insieme a image_tag.
			updateInstallConfKey(a.tlsDirFor(path), "APPS_ENABLED", "true")
			_, _ = fmt.Fprintln(a.Stdout, "Hosting delle app acceso.")
		case upgrade.NoChange:
			// Già alla versione richiesta: lo script gitstack-tls non si aggiorna (lo fa solo un
			// upgrade vero): se è vecchio, applyAppsDomain dice cosa fare.
			if c := a.applyAppsDomainAfterRun(ctx, cfg, path, appsDomainNew, res); c != ExitOK {
				return c
			}
			// Niente helm upgrade dell'aggiornamento, si applicano solo i valori.
			if c := a.helmApps(ctx, cfg, "upgrade", appsSets...); c != ExitOK {
				return c
			}
			if err := config.SetFields(path, appsConfig); err != nil {
				a.errorf("upgrade: hosting delle app acceso ma config.yaml non aggiornato: %v", err)
				return ExitUnexpected
			}
			updateInstallConfKey(a.tlsDirFor(path), "APPS_ENABLED", "true")
			_, _ = fmt.Fprintln(a.Stdout, "Hosting delle app acceso.")
		default:
			// RolledBack, Broken o Failed: il dominio non è stato toccato.
			pendingDomain()
		}
	}
	if *enableDatabases && !*dry {
		if c := a.finishEnableDatabases(ctx, cfg, path, res, dbSets, dbConfig, dbFrom, dbTo); c != ExitOK {
			return c
		}
	}
	a.autoPublish(ctx, cfg, path)
	w := a.Stdout
	if res.Outcome == upgrade.RolledBack || res.Outcome == upgrade.Broken {
		w = a.Stderr
	}
	_, _ = fmt.Fprintln(w)
	for _, l := range res.Summary {
		_, _ = fmt.Fprintln(w, l)
	}
	switch res.Outcome {
	case upgrade.RolledBack:
		return ExitRolledBack
	case upgrade.Broken:
		a.errorf("rollback: %s", res.RollbackErr)
		return ExitBroken
	}
	return ExitOK
}

// mergeConfigFields unisce le chiavi di config.yaml da scrivere con l'aggiornamento (nil se non ce n'è).
func mergeConfigFields(maps ...map[string]string) map[string]string {
	var out map[string]string
	for _, m := range maps {
		for k, v := range m {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// finishEnableDatabases chiude `upgrade --enable-databases` (GIT-557). Con l'aggiornamento riuscito il chart ha
// già i valori e config.yaml li ha scritti upgrade.Run (ExtraConfig); resta install.conf. Se la versione era già
// quella richiesta (NoChange) non c'è stato helm upgrade: si creano i segreti e si applicano i soli valori.
func (a *App) finishEnableDatabases(ctx context.Context, cfg *config.Config, path string, res *upgrade.Result, sets []string, fields map[string]string, from, to int) int {
	switch res.Outcome {
	case upgrade.Upgraded:
	case upgrade.NoChange:
		if c := a.ensureDatabaseSecrets(ctx, cfg, path, "upgrade"); c != ExitOK {
			return c
		}
		if !chartHasDatabases(cfg.ChartDir) {
			a.errorf("upgrade: il chart in %s non conosce i database gestiti: serve un aggiornamento a una versione che li porta.", cfg.ChartDir)
			return ExitConfig
		}
		if c := a.helmSets(ctx, cfg, "upgrade", sets...); c != ExitOK {
			return c
		}
		if err := config.SetFields(path, fields); err != nil {
			a.errorf("upgrade: database gestiti accesi ma config.yaml non aggiornato: %v", err)
			return ExitUnexpected
		}
	default:
		return ExitOK
	}
	updateInstallConfKey(a.tlsDirFor(path), "DATABASES_ENABLED", "true")
	if from > 0 && to > 0 {
		updateInstallConfKey(a.tlsDirFor(path), "DATABASES_PORT_RANGE", fmt.Sprintf("%d-%d", from, to))
	}
	_, _ = fmt.Fprintln(a.Stdout, "Database gestiti accesi.")
	a.databasesFirewallHint(from, to)
	return ExitOK
}

// applyAppsDomainAfterRun applica il dominio delle app dopo un upgrade.Run riuscito (GIT-559),
// quando gitstack-tls è già quello della destinazione. Se fallisce, l'aggiornamento è comunque
// finito: lo dice, stampa il riepilogo e il comando da lanciare.
func (a *App) applyAppsDomainAfterRun(ctx context.Context, cfg *config.Config, path, domain string, res *upgrade.Result) int {
	if domain == "" {
		return ExitOK
	}
	c := a.applyAppsDomain(ctx, cfg, path, domain, "upgrade")
	if c == ExitOK {
		return c
	}
	next := "i valori del chart apps.enabled/apps.domain sono già applicati"
	if res.Outcome != upgrade.Upgraded {
		next = "poi lancia `gitstack config set apps on` per accendere l'hosting"
	}
	_, _ = fmt.Fprintf(a.Stderr, "Il dominio delle app NON è stato applicato (lo script gitstack-tls o il certificato lo hanno rifiutato). Sistemata la causa lancia `gitstack config set apps-domain %s`; %s.\n", domain, next)
	for _, l := range res.Summary {
		_, _ = fmt.Fprintln(a.Stderr, l)
	}
	return c
}

func (a *App) exePath() string {
	if a.ExePath != "" {
		return a.ExePath
	}
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}
