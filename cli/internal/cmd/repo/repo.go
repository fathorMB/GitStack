// Package repo è il gruppo `gs repo` (G4): create, list, view, clone, edit,
// archive, unarchive e delete, sopra le API di M-03 (R1, R2, R5, R10,
// R12, P7).
//
// Output JSON (--json, --jq): i campi sono quelli dello schema `Repository`
// dell'API, più `url` (pagina web). Sono elencati in RepoFields e documentati
// in cli/README.md.
package repo

import (
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// RepoFields sono i campi di --json di create, list, view, edit, archive,
// e unarchive.
var RepoFields = []string{
	"id", "name", "owner", "fullName", "description", "visibility", "archived", "archivedAt",
	"defaultBranch", "empty", "protectDefaultBranch", "cloneUrls", "mirror", "createdAt", "updatedAt", "url",
}

// repoOut è un repo con il suo indirizzo web.
type repoOut struct {
	gitstack.Repository
	URL string `json:"url"`
}

// NewCmd restituisce il comando padre `gs repo`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Gestisci i repository",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newCreateCmd(f),
		newListCmd(f),
		newViewCmd(f),
		newCloneCmd(f),
		newEditCmd(f),
		newArchiveCmd(f),
		newUnarchiveCmd(f),
		newDeleteCmd(f),
	)
	return cmd
}
