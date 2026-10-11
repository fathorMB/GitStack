// Package repo è il gruppo `gs repo` (G4): create, list, view, clone, edit,
// archive, unarchive, delete e restore, sopra le API di M-03 (R1, R2, R5, R10,
// R12, P7).
//
// Output JSON (--json, --jq): i campi sono quelli dello schema `Repository`
// dell'API, più `url` (pagina web). Sono elencati in RepoFields e documentati
// in cli/README.md. `list --deleted` produce `DeletedRepository` (più
// `fullName` e `url`), con i campi di DeletedFields.
package repo

import (
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// RepoFields sono i campi di --json di create, list, view, edit, archive,
// unarchive e restore.
var RepoFields = []string{
	"id", "name", "owner", "fullName", "description", "visibility", "archived", "archivedAt",
	"defaultBranch", "empty", "protectDefaultBranch", "cloneUrls", "createdAt", "updatedAt", "url",
}

// DeletedFields sono i campi di --json di `gs repo list --deleted`.
var DeletedFields = []string{"id", "name", "owner", "fullName", "deletedAt", "purgeAt", "url"}

// ListFields è l'unione ammessa da `gs repo list`: i campi di --deleted sono
// `null` nell'elenco normale e viceversa.
var ListFields = unionFields(RepoFields, DeletedFields)

func unionFields(a, b []string) []string {
	out := append([]string{}, a...)
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}

// repoOut è un repo con il suo indirizzo web.
type repoOut struct {
	gitstack.Repository
	URL string `json:"url"`
}

type deletedOut struct {
	gitstack.DeletedRepository
	FullName string `json:"fullName"`
	URL      string `json:"url"`
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
		newRestoreCmd(f),
	)
	return cmd
}
