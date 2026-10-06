// Package milestone è il gruppo `gs milestone`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package milestone

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs milestone`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "milestone",
		Short: "Gestisci le milestone",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
