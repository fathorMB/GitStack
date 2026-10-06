// Package milestone è il gruppo `gs milestone` (G4): list, view, create,
// edit, close, reopen, delete, sopra le API di M-05 (I7).
//
// Output JSON (--json, --jq): i campi sono quelli dello schema `Milestone`
// dell'API, più `url` (pagina web), `progress` (avanzamento). Sono
// elencati in MilestoneFields e documentati in cli/README.md.
package milestone

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// MilestoneFields sono i campi di --json di list, view, create, edit.
var MilestoneFields = []string{"id", "number", "title", "description", "state", "dueOn", "openIssues", "closedIssues", "closedAt", "createdAt", "updatedAt", "url", "progress"}

// NewCmd restituisce il comando padre `gs milestone`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "milestone",
		Short: "Gestisci le milestone",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newListCmd(f),
		newViewCmd(f),
		newCreateCmd(f),
		newEditCmd(f),
		newCloseCmd(f),
		newReopenCmd(f),
		newDeleteCmd(f),
	)
	return cmd
}
