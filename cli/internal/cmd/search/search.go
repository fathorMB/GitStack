// Package search è il gruppo `gs search` (G4): `issues` cerca su tutta
// l'installazione con la sintassi I10 (GET /search/issues) e `code` cerca un
// testo nel codice di un repo (B5, GET /repos/{owner}/{repo}/search, al
// massimo 100 risultati).
//
// Output JSON: `issues` stampa una lista di IssueSummary con `repo`
// (`owner/nome`) e `url`; `code` stampa un oggetto con i campi di
// CodeSearchResult (`query`, `ref`, `limitReached`, `timedOut`, `results`);
// ogni risultato ha `path`, `line`, `fragment` e `url`. I campi sono
// elencati in IssueFields e CodeFields e documentati in cli/README.md.
package search

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs search`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Cerca issue e codice",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(newIssuesCmd(f), newCodeCmd(f))
	return cmd
}
