package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/fathorMB/GitStack/admin/internal/dbpki"
)

// runDatabases è `gitstack databases ensure`: crea la CA intermedia dei database e la chiave di dump se
// mancano (ADR 0003, GIT-565). La lancia deploy/install.sh dopo l'installazione con databases.enabled;
// `gitstack upgrade` fa lo stesso da solo. Idempotente: non tocca quello che c'è.
func runDatabases(ctx context.Context, a *App, args []string) int {
	if len(args) == 0 || args[0] != "ensure" {
		a.errorf("databases: sottocomando atteso: ensure")
		a.usage(a.Stderr)
		return ExitUsage
	}
	fs := flag.NewFlagSet("databases ensure", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("databases ensure: %v", err)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	cl, err := a.cluster(cfg)
	if err != nil {
		a.errorf("databases ensure: %v", err)
		return ExitCluster
	}
	res, err := dbpki.Ensure(ctx, dbpki.Options{Cluster: cl, Release: cfg.Release, TLSDir: a.tlsDirFor(path)})
	switch {
	case errors.Is(err, dbpki.ErrNoRootCA):
		if res.CreatedDumpKey {
			_, _ = fmt.Fprintf(a.Stdout, "Chiave di dump creata (%s).\n", dbpki.DumpKeySecretName(cfg.Release))
		}
		a.errorf("databases ensure: CA intermedia non creata: %v", err)
		return ExitConfig
	case err != nil:
		a.errorf("databases ensure: %v", err)
		return ExitUnexpected
	}
	report := func(created bool, what, name string) {
		if created {
			_, _ = fmt.Fprintf(a.Stdout, "%s creata (%s).\n", what, name)
		} else {
			_, _ = fmt.Fprintf(a.Stdout, "%s già presente (%s): invariata.\n", what, name)
		}
	}
	report(res.CreatedDumpKey, "Chiave di dump", dbpki.DumpKeySecretName(cfg.Release))
	report(res.CreatedCA, "CA intermedia dei database", dbpki.CASecretName(cfg.Release))
	return ExitOK
}
