// Package app è il gruppo `gs app`. Per ora porta `gs app access` (GIT-376, H6): la lista di accesso
// dell'app per i tester esterni e l'invito.
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
	cmd.AddCommand(newAccessCmd(f))
	return cmd
}
