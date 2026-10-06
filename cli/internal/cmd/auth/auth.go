// Package auth è il gruppo `gs auth`. Per ora solo il comando padre: i
// sottocomandi arrivano con gli item successivi di M-07, che toccano solo
// questa cartella.
package auth

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs auth`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	_ = f
	return &cobra.Command{
		Use:   "auth",
		Short: "Autenticazione: login, logout, stato e token",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
}
