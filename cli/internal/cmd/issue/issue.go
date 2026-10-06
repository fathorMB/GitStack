// Package issue è il gruppo `gs issue` (G4): create, list, view, edit,
// comment, close, reopen, lock e unlock, sopra le API di M-05.
//
// Output JSON (--json, --jq): i campi sono quelli dello schema dell'API
// (`Issue` per create, view, edit, close, reopen, lock, unlock; `IssueSummary`
// per list; `IssueComment` per comment), più `url` (pagina web). Sono
// elencati in IssueFields, SummaryFields e CommentFields e documentati in
// cli/README.md.
package issue

import (
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// IssueFields sono i campi di --json di create, edit, close, reopen, lock e
// unlock; view aggiunge `comments`.
var IssueFields = []string{
	"id", "number", "title", "body", "state", "closeReason", "duplicateOf", "author",
	"viaToken", "labels", "assignees", "milestone", "locked", "hidden", "edited",
	"commentCount", "attachments", "closedAt", "createdAt", "updatedAt", "url",
}

// ViewFields sono i campi di --json di `gs issue view`.
var ViewFields = append(append([]string{}, IssueFields...), "comments")

// SummaryFields sono i campi di --json di `gs issue list`.
var SummaryFields = []string{
	"number", "title", "state", "closeReason", "author", "labels", "assignees",
	"milestone", "locked", "commentCount", "createdAt", "updatedAt", "url",
}

// CommentFields sono i campi di --json di `gs issue comment`.
var CommentFields = []string{
	"id", "issueNumber", "body", "author", "viaToken", "edited", "deleted",
	"attachments", "createdAt", "updatedAt", "url",
}

// issueOut è una issue con il suo indirizzo web (e i commenti, in view).
type issueOut struct {
	gitstack.Issue
	URL      string                   `json:"url"`
	Comments *[]gitstack.IssueComment `json:"comments,omitempty"`
}

type summaryOut struct {
	gitstack.IssueSummary
	URL string `json:"url"`
}

type commentOut struct {
	gitstack.IssueComment
	URL string `json:"url"`
}

// NewCmd restituisce il comando padre `gs issue`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Gestisci le issue",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newCreateCmd(f),
		newListCmd(f),
		newViewCmd(f),
		newEditCmd(f),
		newCommentCmd(f),
		newCloseCmd(f),
		newReopenCmd(f),
		newLockCmd(f),
		newUnlockCmd(f),
	)
	return cmd
}
