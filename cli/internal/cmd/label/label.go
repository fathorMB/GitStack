// Package label è il gruppo `gs label` (G4): list, create, edit e delete,
// sopra le API di M-05 (I3, I5).
//
// Output JSON (--json, --jq): i campi sono quelli dello schema `Label`
// dell'API, più `url` (pagina web). Sono elencati in LabelFields e
// documentati in cli/README.md.
package label

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// LabelFields sono i campi di --json di list, create, edit.
var LabelFields = []string{"id", "name", "color", "description", "openIssues", "url"}

// NewCmd restituisce il comando padre `gs label`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Gestisci le etichette",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newListCmd(f),
		newCreateCmd(f),
		newEditCmd(f),
		newDeleteCmd(f),
	)
	return cmd
}
