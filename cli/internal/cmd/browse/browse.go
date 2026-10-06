// Package browse è il comando foglia `gs browse`, non ancora implementato.
package browse

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce `gs browse`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:                "browse",
		Short:              "Apri una pagina del repository nel browser",
		DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error {
			return cmdutil.UsageErrorf("gs browse: non ancora implementato")
		},
	}
}
