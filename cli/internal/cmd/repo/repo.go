// Package repo è il gruppo `gs repo`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package repo

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs repo`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "repo",
		Short: "Gestisci i repository",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
