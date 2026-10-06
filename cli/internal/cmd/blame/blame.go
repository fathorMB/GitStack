// Package blame è il comando foglia `gs blame`, non ancora implementato.
package blame

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce `gs blame`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:                "blame",
		Short:              "Mostra chi ha modificato ogni riga di un file",
		DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error {
			return cmdutil.UsageErrorf("gs blame: non ancora implementato")
		},
	}
}
