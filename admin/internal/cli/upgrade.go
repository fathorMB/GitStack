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
	dest := fs.String("dest", "", "cartella del backup preventivo (default backup.destination del config)")
	keyFile := fs.String("key-file", "", "file con la chiave per cifrare il backup preventivo (AES-256-GCM)")
	timeout := fs.Duration("timeout", upgrade.DefaultTimeout, "tempo massimo per l'aggiornamento dei servizi e per la loro salute")
	dry := fs.Bool("dry-run", false, "esegue solo i controlli sulla destinazione, senza modificare nulla")
	var sets, values multiFlag
	fs.Var(&sets, "set", "valore del chart (chiave=valore), ripetibile")
	fs.Var(&values, "values", "file di valori del chart, ripetibile")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("upgrade: %v", err)
		a.usage(a.Stderr)
		return ExitUsage
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
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = readKey(*keyFile); err != nil {
			a.errorf("upgrade: %v", err)
			return ExitUsage
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
		Cfg:        cfg,
		ConfigFile: bo.ConfigFile,
		Cluster:    cl,
		Runner:     a.Runner,
		Health:     func(ctx context.Context) *status.Report { return col.Collect(ctx, cfg, configDir) },
		Backup:     *bo,
		To:         *to,
		DryRun:     *dry,
		Timeout:    *timeout,
		Sets:       sets,
		ValueFiles: values,
		HelmBin:    a.helmBin(),
		ExePath:    a.exePath(),
		Repo:       a.Getenv("GITSTACK_REPO"),
		Ref:        a.Getenv("GITSTACK_REF"),
		Log:        a.Stdout,
	}
	if status.ServesCA(cfg) {
		o.CheckCA = func(ctx context.Context) error { return status.CheckCA(ctx, cfg, nil) }
	}
	if a.TuneUpgrade != nil {
		a.TuneUpgrade(o)
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
