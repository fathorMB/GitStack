// Package search è il gruppo `gs search`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package search

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs search`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "search",
		Short: "Cerca repository, issue e codice",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
