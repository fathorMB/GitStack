package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/pgcheck"
)

// ExitPostgres: il preflight del Postgres esterno non è superato.
const ExitPostgres = 9

const envPostgresPassword = "GITSTACK_POSTGRES_PASSWORD"

func runPostgres(ctx context.Context, a *App, args []string) int {
	if len(args) == 0 || args[0] != "check" {
		a.errorf("uso: gitstack postgres check --url postgres://utente@host:5432/database [--password-file FILE] [--ca-file FILE] [--json]")
		return ExitUsage
	}
	fs := flag.NewFlagSet("postgres check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	url := fs.String("url", a.Getenv("GITSTACK_POSTGRES_URL"), "URL del database")
	pwFile := fs.String("password-file", a.Getenv("GITSTACK_POSTGRES_PASSWORD_FILE"), "file con la password")
	ca := fs.String("ca-file", a.Getenv("GITSTACK_POSTGRES_CA_FILE"), "CA del database (PEM)")
	asJSON := fs.Bool("json", false, "stampa l'esito in JSON")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("postgres check: %v", err)
		return ExitUsage
	}
	pw := a.Getenv(envPostgresPassword)
	if *pwFile != "" {
		b, err := os.ReadFile(*pwFile)
		if err != nil {
			a.errorf("file della password: %v", err)
			return ExitPostgres
		}
		pw = strings.TrimRight(string(b), "\r\n")
	}
	if *ca != "" {
		if _, err := os.Stat(*ca); err != nil {
			a.errorf("CA del database %s: %v", *ca, err)
			return ExitPostgres
		}
	}
	res, err := pgcheck.Check(ctx, pgcheck.Params{URL: *url, Password: pw, CAFile: *ca})
	if err != nil {
		var f *pgcheck.Failure
		if errors.As(err, &f) {
			if *asJSON {
				_ = json.NewEncoder(a.Stdout).Encode(map[string]any{"ok": false, "kind": f.Kind, "error": f.Msg})
			}
			a.errorf("Postgres esterno: %s", f.Msg)
			return ExitPostgres
		}
		a.errorf("postgres check: %v", err)
		return ExitUnexpected
	}
	if *asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"ok": true, "result": res})
		return ExitOK
	}
	tls := "no"
	if res.TLS {
		tls = "sì"
	}
	_, _ = fmt.Fprintf(a.Stdout, "PostgreSQL %s su %s:%d, database %s, utente %s (TLS: %s)\n", res.Version, res.Host, res.Port, res.Database, res.User, tls)
	_, _ = fmt.Fprintf(a.Stdout, "Versione supportata (%d-%d), connessione riuscita, può creare schemi e ruoli.\n", pgcheck.MinMajor, pgcheck.MaxMajor)
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(a.Stdout, "AVVISO: %s\n", w)
	}
	return ExitOK
}
