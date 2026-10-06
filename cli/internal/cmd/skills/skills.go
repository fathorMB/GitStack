// Package skills è il gruppo `gs skills`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package skills

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs skills`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "skills",
		Short: "Installa le skills per gli agenti di coding",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
