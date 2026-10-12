package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/fathorMB/GitStack/admin/internal/status"
)

func runVersion(_ context.Context, a *App, _ []string) int {
	_, _ = fmt.Fprintf(a.Stdout, "gitstack %s\n", a.Version)
	return ExitOK
}

func runStatus(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	asJSON := fs.Bool("json", false, "stampa il report in JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("status: %v", err)
		a.usage(a.Stderr)
		return ExitUsage
	}
	cfg, code := a.loadConfig(a.configPath(*cfgPath))
	if cfg == nil {
		return code
	}

	path := a.configPath(*cfgPath)
	col := &status.Collector{Runner: a.Runner, HTTP: a.HTTP, LookPath: a.LookPath}
	rep := col.Collect(ctx, cfg, a.configDir(path))
	a.k3sAvailable(ctx, rep)

	if *asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Binary string         `json:"binary_version"`
			Report *status.Report `json:"report"`
		}{a.Version, rep}); err != nil {
			a.errorf("status: %v", err)
			return ExitUnexpected
		}
	} else {
		printReport(a.Stdout, a.Version, rep)
	}

	switch {
	case rep.ClusterError != "":
		return ExitCluster
	case !rep.Healthy():
		return ExitUnhealthy
	}
	return ExitOK
}

func printDatabase(p func(string, ...any), d status.Database) {
	ver := d.Version
	switch {
	case ver == "":
		ver = "sconosciuta"
	case !d.VersionLive:
		ver += " (rilevata all'installazione)"
	}
	if d.Mode != "external" {
		p("  Postgres interno (bundle)    versione %s", ver)
		return
	}
	p("  Postgres ESTERNO             %s:%d/%s (utente %s, sslmode %s)", d.Host, d.Port, d.Name, d.User, d.SSLMode)
	p("  Versione PostgreSQL          %s", ver)
	if d.Error != "" {
		p("  ATTENZIONE: %s", d.Error)
	}
	if d.Warning != "" {
		p("  AVVISO: %s", d.Warning)
	}
}

func printReport(w io.Writer, binVersion string, r *status.Report) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	p("GitStack")
	p("  Versione server: %s", r.ServerVersion)
	p("  Versione gitstack: %s", binVersion)
	p("  Host: %s (SSH git: porta %d)", r.Host, r.SSHPort)
	p("  Release: %s (namespace %s)", r.Release, r.Namespace)
	switch {
	case r.K3sVersion == "":
		p("  Versione k3s: non leggibile (k3s non è nel PATH)")
	case r.K3sUpdate == "patch":
		p("  Versione k3s: %s — aggiornamento disponibile: %s (patch, si applica da solo con gitstack upgrade)", r.K3sVersion, r.K3sDeclared)
	case r.K3sUpdate == "minor":
		p("  Versione k3s: %s — aggiornamento disponibile: %s (nuova minore: gitstack upgrade --k3s)", r.K3sVersion, r.K3sDeclared)
	default:
		p("  Versione k3s: %s (aggiornata)", r.K3sVersion)
	}
	p("")
	p("Servizi")
	if r.ClusterError != "" {
		p("  cluster non interrogabile: %s", r.ClusterError)
	} else if len(r.Services) == 0 {
		p("  nessun servizio trovato per la release %s nel namespace %s", r.Release, r.Namespace)
	}
	for _, s := range r.Services {
		state := "OK"
		if !s.Healthy() {
			state = "NON PRONTO"
		}
		p("  %-28s %d/%d pronti  %s", s.Name, s.Ready, s.Desired, state)
	}
	api := "OK"
	if !r.APIHealthy {
		api = "NON RAGGIUNGIBILE"
	}
	p("  %-28s %s (%s)", "api (gateway /healthz)", api, r.APIDetail)
	p("")
	p("Database")
	printDatabase(p, r.Database)
	p("")
	p("Ultimo backup: %s", r.LastBackup)
	if r.BackupUpload != "" {
		p("Backup su S3: %s", r.BackupUpload)
	}
	// V5: catena di fornitura.
	if r.V5Warning != "" {
		p("")
		p("Catena di fornitura (V5)")
		p("  AVVISO: %s", r.V5Warning)
	}
	p("")
	switch {
	case r.ClusterError != "":
		p("Stato: cluster non interrogabile")
	case r.Healthy():
		p("Stato: sano")
	default:
		p("Stato: NON sano")
	}
}
