// Package issue è il gruppo `gs issue`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package issue

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs issue`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "issue",
		Short: "Gestisci le issue",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
