// Package app è il gruppo `gs app`. Porta il giro di un'app (GIT-354, H13): list, create, deploy, status, logs,
// restart, rollback, scale, env e secret, più `gs app access` (GIT-376, H6). Stessa API della UI, con --json e
// codici di uscita G3.
package app

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs app`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Gestisci le app interne",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(newListCmd(f), newCreateCmd(f), newDeployCmd(f), newStatusCmd(f), newLogsCmd(f),
		newRestartCmd(f), newDeleteCmd(f), newRollbackCmd(f), newScaleCmd(f), newEnvCmd(f), newSecretCmd(f), newAccessCmd(f), newVolumeCmd(f), newDomainCmd(f))
	return cmd
}
