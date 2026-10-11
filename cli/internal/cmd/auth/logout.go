package auth

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
)

func newLogoutCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Esci da un'istanza: toglie il token salvato e la configurazione",
		Long: "Toglie il token (portachiavi e file) e la configurazione dell'istanza di --hostname, di GS_HOST\n" +
			"o, in mancanza, di quella predefinita. Il token non viene revocato sul server: si fa dalla UI.",
		Args: cmdutil.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			host := normalizeHost(f.HostnameFlag)
			if host == "" {
				host = normalizeHost(f.Getenv(cmdutil.EnvHost))
			}
			if host == "" {
				if host, err = cfg.DefaultHost(); err != nil {
					return err
				}
			}
			if host == "" {
				return cmdutil.NotAuthenticatedError("nessuna istanza configurata")
			}
			stored, err := f.StoredTokens()
			if err != nil {
				return err
			}
			_, _, tokErr := stored.Token(host)
			_, known, err := cfg.Host(host)
			if err != nil {
				return err
			}
			if tokErr != nil && !known {
				if f.Getenv(config.EnvToken) != "" {
					return cmdutil.NotAuthenticatedError("nessun login salvato per %s: il token arriva da %s, togli la variabile", host, config.EnvToken)
				}
				return cmdutil.NotAuthenticatedError("nessun login salvato per %s", host)
			}
			if err := stored.DeleteToken(host); err != nil {
				return err
			}
			if err := cfg.DeleteHost(host); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Uscito da %s\n", host)
			return nil
		},
	}
}
