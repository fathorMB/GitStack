// Package version è il comando `gs version`.
package version

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// Line è la riga stampata da `gs version` e `gs --version`.
func Line(version string) string { return "gs version " + version }

// NewCmd restituisce `gs version`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Mostra la versione di gs",
		Args:  cmdutil.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintln(f.IO.Out, Line(f.Version))
			return err
		},
	}
}
