// Package label è il gruppo `gs label`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package label

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs label`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "label",
		Short: "Gestisci le etichette",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
