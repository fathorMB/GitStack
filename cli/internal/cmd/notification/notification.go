// Package notification è il gruppo `gs notification`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package notification

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs notification`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "notification",
		Short: "Leggi e gestisci le notifiche",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
