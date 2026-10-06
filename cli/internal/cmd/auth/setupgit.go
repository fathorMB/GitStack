package auth

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

func newSetupGitCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "setup-git",
		Short: "Configura git perché usi gs come credential helper in HTTPS",
		Long: "Per ogni istanza configurata (o solo per --hostname, o GS_HOST) scrive nella configurazione\n" +
			"globale di git `credential.<url>.helper` con `gs auth git-credential`: clone, fetch e push in\n" +
			"HTTPS usano il token di gs senza chiederlo. Si può ripetere: sostituisce la voce precedente.",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			hosts, err := setupHosts(f)
			if err != nil {
				return err
			}
			exe, err := f.Executable()
			if err != nil {
				return fmt.Errorf("percorso di gs: %w", err)
			}
			helper := "!" + shellQuote(exe) + " auth git-credential"
			for _, h := range hosts {
				key := "credential." + webURL(h) + ".helper"
				// La voce vuota azzera gli helper ereditati (come fa gh), poi si aggiunge il nostro.
				if err := f.RunGit(c.Context(), "config", "--global", "--replace-all", key, ""); err != nil {
					return err
				}
				if err := f.RunGit(c.Context(), "config", "--global", "--add", key, helper); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(f.IO.ErrOut, "git userà gs per %s\n", webURL(h))
			}
			return nil
		},
	}
}

func setupHosts(f *cmdutil.Factory) ([]string, error) {
	if h := normalizeHost(f.HostnameFlag); h != "" {
		return []string{h}, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	hosts, err := cfg.Hosts()
	if err != nil {
		return nil, err
	}
	if h := normalizeHost(f.Getenv(cmdutil.EnvHost)); h != "" && !contains(hosts, h) {
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return nil, cmdutil.NotAuthenticatedError("nessuna istanza configurata: esegui `gs auth login` o usa --hostname")
	}
	return hosts, nil
}

// shellQuote mette p fra apici singoli per la shell di git (sh).
func shellQuote(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
