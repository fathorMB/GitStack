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
	authMW := fs.String("auth-middleware", "", "con --enable-apps: Middleware Traefik forward-auth del login (nome o namespace/nome)")
	var sets, values multiFlag
	fs.Var(&sets, "set", "valore del chart (chiave=valore), ripetibile")
	fs.Var(&values, "values", "file di valori del chart, ripetibile")
	allowUnsigned := fs.Bool("allow-unsigned-mirrors", false, "eccezione N9: non verificare le firme cosign (mirror interno non firmato)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("upgrade: %v", err)
		a.usage(a.Stderr)
		return ExitUsage
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
	if (*appsDomain != "" || *authMW != "") && !*enableApps {
		a.errorf("upgrade: --apps-domain e --auth-middleware valgono solo con --enable-apps")
		return ExitUsage
	}
	if *authMW != "" && !appsMiddlewareRe.MatchString(*authMW) {
		a.errorf("upgrade: --auth-middleware %q non valido (nome o namespace/nome)", *authMW)
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
		if *authMW != "" {
			appsSets = append(appsSets, "apps.authMiddleware="+*authMW)
		}
		if d != cfg.AppsDomain {
			appsDomainNew = d
		}
		if *dry {
			_, _ = fmt.Fprintf(a.Stdout, "==> --dry-run: l'hosting delle app si accenderebbe con il dominio %s.\n", d)
		}
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
		Cfg:           cfg,
		ConfigFile:    bo.ConfigFile,
		Cluster:       cl,
		Runner:        a.Runner,
		Health:        func(ctx context.Context) *status.Report { return col.Collect(ctx, cfg, configDir) },
		Backup:        *bo,
		To:            *to,
		DryRun:        *dry,
		AllowK3sMinor: *k3sMinor,
		Timeout:       *timeout,
		Sets:          append(append([]string{}, sets...), appsSets...),
		ExtraConfig:   appsConfig,
		ValueFiles:    values,
		HelmBin:       a.helmBin(),
		ExePath:       a.exePath(),
		Repo:          a.Getenv("GITSTACK_REPO"),
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

	// Il dominio nuovo (e il suo certificato wildcard) prima dell'aggiornamento, mai in --dry-run:
	// se l'upgrade poi si ferma, il dominio resta impostato come con «config set apps-domain».
	if appsDomainNew != "" && !*dry {
		if c := a.applyAppsDomain(ctx, cfg, path, appsDomainNew, "upgrade"); c != ExitOK {
			return c
		}
	}

	res, err := upgrade.Run(ctx, o)
	var ref *upgrade.RefusedError
	switch {
	case errors.As(err, &ref):
		a.errorf("upgrade rifiutato: %v", err)
		_, _ = fmt.Fprintln(a.Stderr, "Niente è stato modificato.")
		return ExitRefused
	case err != nil:
		a.errorf("upgrade: %v", err)
		for _, l := range res.Summary {
			_, _ = fmt.Fprintln(a.Stderr, l)
		}
		return ExitUnexpected
	}
	if *enableApps && !*dry {
		switch res.Outcome {
		case upgrade.Upgraded:
			// config.yaml l'ha già scritto l'upgrade (ExtraConfig), insieme a image_tag.
			updateInstallConfKey(a.tlsDirFor(path), "APPS_ENABLED", "true")
			_, _ = fmt.Fprintln(a.Stdout, "Hosting delle app acceso.")
		case upgrade.NoChange:
			// Già alla versione richiesta: niente helm upgrade dell'aggiornamento, si applicano solo i valori.
			if c := a.helmApps(ctx, cfg, "upgrade", appsSets...); c != ExitOK {
				return c
			}
			if err := config.SetFields(path, appsConfig); err != nil {
				a.errorf("upgrade: hosting delle app acceso ma config.yaml non aggiornato: %v", err)
				return ExitUnexpected
			}
			updateInstallConfKey(a.tlsDirFor(path), "APPS_ENABLED", "true")
			_, _ = fmt.Fprintln(a.Stdout, "Hosting delle app acceso.")
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
