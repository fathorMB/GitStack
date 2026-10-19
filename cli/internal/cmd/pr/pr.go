// Package pr è il gruppo `gs pr` (PR10, GIT-270): create, list, view, checkout,
// diff, review, comment, ready, merge, close e status, sopra le API delle Pull
// Request (PR1-PR12). Le regole le applica il server: `gs` mostra l'errore e lo
// traduce nel codice di uscita (G3). Merge e chiusura chiedono conferma (G8).
//
// Output JSON (--json, --jq): i campi sono quelli dello schema dell'API
// (`PullRequest` per view, create, ready, merge, close, checkout; `PullRequestSummary`
// per list; `PullRequestReview`, `PullRequestComment`, `PullRequestFiles` per
// review, comment e diff), più `url` (pagina web) dove c'è una PR. Sono elencati
// in PRFields, SummaryFields e così via, e documentati in cli/README.md.
package pr

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

// PRFields sono i campi di --json di view, create, ready, merge, close e checkout.
var PRFields = []string{
	"id", "number", "title", "body", "state", "draft", "author", "viaToken",
	"sourceBranch", "targetBranch", "headSha", "baseSha", "sourceBranchExists",
	"requestedReviewers", "reviewDecision", "approvals", "requiredApprovals", "statusState",
	"commentCount", "unresolvedThreadCount", "closingIssues", "commitCount", "changedFiles",
	"additions", "deletions", "mergeMethod", "mergeCommitSha", "mergedBy", "mergedAt",
	"closedAt", "createdAt", "updatedAt", "url",
}

// SummaryFields sono i campi di --json di list e status.
var SummaryFields = []string{
	"number", "title", "state", "draft", "author", "sourceBranch", "targetBranch",
	"reviewDecision", "statusState", "requestedReviewers", "commentCount",
	"mergedAt", "closedAt", "createdAt", "updatedAt", "url",
}

// StatusFields sono i campi di --json di status: la PR del ramo corrente
// (o null) e le PR aperte di chi chiama.
var StatusFields = []string{"current", "authored"}

// ReviewFields sono i campi di --json di review.
var ReviewFields = []string{
	"id", "author", "state", "body", "commitSha", "stale", "countsAsApproval",
	"commentCount", "submittedAt",
}

// CommentFields sono i campi di --json di comment.
var CommentFields = []string{
	"id", "author", "body", "path", "line", "side", "startLine", "commitSha",
	"outdated", "edited", "deleted", "viaToken", "createdAt", "updatedAt", "url",
}

// FilesFields sono i campi di --json di diff.
var FilesFields = []string{
	"files", "filesChanged", "additions", "deletions", "truncated", "listOnly",
	"ignoreWhitespace", "viewedPaths", "headSha", "baseSha", "url",
}

// prOut è una PR con il suo indirizzo web.
type prOut struct {
	gitstack.PullRequest
	URL string `json:"url"`
}

// summaryOut è una PR negli elenchi con il suo indirizzo web.
type summaryOut struct {
	gitstack.PullRequestSummary
	URL string `json:"url"`
}

// commentOut è un commento sulla PR con l'indirizzo della PR.
type commentOut struct {
	gitstack.PullRequestComment
	URL string `json:"url"`
}

// filesOut è il diff della PR con l'indirizzo della PR.
type filesOut struct {
	gitstack.PullRequestFiles
	URL string `json:"url"`
}

// statusOut è la risposta di status.
type statusOut struct {
	Current  *summaryOut  `json:"current"`
	Authored []summaryOut `json:"authored"`
}

// NewCmd restituisce il comando padre `gs pr`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Gestisci le Pull Request",
		Long: "Gestisci le Pull Request del repo (origine e destinazione sono due branch dello stesso repo). " +
			"Il repo è quello del remote `origin` o `-R owner/repo`. Chi li usa passa i numeri come `7` o `#7`.\n\n" +
			"Flusso tipico: `gs pr create --draft` a lavoro iniziato, poi `gs pr ready --reviewer <nome>` quando i test sono verdi. " +
			"`merge` e `close` chiedono conferma (G8): con --yes procedono, senza terminale esce con 2.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newCreateCmd(f),
		newListCmd(f),
		newViewCmd(f),
		newCheckoutCmd(f),
		newDiffCmd(f),
		newReviewCmd(f),
		newCommentCmd(f),
		newReadyCmd(f),
		newMergeCmd(f),
		newCloseCmd(f),
		newStatusCmd(f),
	)
	return cmd
}

// parseNumber legge il numero di una PR: `7`, `#7` o l'indirizzo web
// (`.../pulls/7`).
func parseNumber(arg string) (int64, error) {
	s := strings.TrimSpace(arg)
	if i := strings.LastIndex(s, "/pulls/"); i >= 0 {
		s = s[i+len("/pulls/"):]
		s = strings.TrimRight(strings.SplitN(strings.SplitN(s, "#", 2)[0], "?", 2)[0], "/")
	}
	s = strings.TrimPrefix(s, "#")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, cmdutil.UsageErrorf("numero di Pull Request non valido: %q (atteso `7` o `#7`)", arg)
	}
	return n, nil
}

// prArg valida il solo argomento `<numero>` e lo legge.
func prArg(c *cobra.Command, args []string) (int64, error) {
	if len(args) != 1 {
		return 0, cmdutil.UsageErrorf("serve il numero della Pull Request: %s <numero>", c.CommandPath())
	}
	return parseNumber(args[0])
}

// readText dà il testo di -b/--body o -F/--body-file (`-` = stdin). I due
// insieme sono un uso errato. set dice se uno dei due era presente.
func readText(f *cmdutil.Factory, body string, bodySet bool, file string) (text string, set bool, err error) {
	if bodySet && file != "" {
		return "", false, cmdutil.UsageErrorf("--body e --body-file sono alternativi")
	}
	if file == "" {
		return body, bodySet, nil
	}
	var b []byte
	if file == "-" {
		b, err = io.ReadAll(f.IO.In)
	} else {
		b, err = os.ReadFile(file)
	}
	if err != nil {
		return "", false, fmt.Errorf("lettura del testo da %s: %w", file, err)
	}
	return string(b), true, nil
}

// webURL è l'indirizzo della pagina web della PR.
func webURL(e *repoenv.Env, n int64) string {
	return fmt.Sprintf("%s/%s/%s/pulls/%d", repoenv.WebBase(e.Host), e.Owner, e.Repo, n)
}

// unexpected è l'errore di una risposta 2xx senza il corpo atteso.
func unexpected(status int) error { return repoenv.Unexpected(status) }

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

// currentBranch dà il ramo corrente; fuori da un ramo (HEAD staccato) è un
// uso errato, con la richiesta di --head o di --head esplicito.
func currentBranch(c *cobra.Command, f *cmdutil.Factory) (string, error) {
	if f.GitOutput == nil {
		return "", cmdutil.UsageErrorf("ramo corrente non determinabile: usa --head <ramo>")
	}
	out, err := f.GitOutput(c.Context(), "branch", "--show-current")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", cmdutil.UsageErrorf("non sei su un ramo (HEAD staccato o fuori da un repo): usa --head <ramo>")
	}
	return strings.TrimSpace(out), nil
}

// printPR scrive una PR: JSON se richiesto, altrimenti l'indirizzo web su stdout.
func printPR(f *cmdutil.Factory, jo *output.JSONOptions, e *repoenv.Env, pr *gitstack.PullRequest) error {
	if jo.Enabled() {
		return jo.Write(f.IO.Out, prOut{PullRequest: *pr, URL: webURL(e, pr.Number)}, f.IO.OutTTY)
	}
	_, err := fmt.Fprintln(f.IO.Out, webURL(e, pr.Number))
	return err
}

// stateLabel è lo stato leggibile di una PR: `open`, `bozza`, `closed`, `merged`.
func stateLabel(state gitstack.PullRequestState, draft bool) string {
	if state == gitstack.PullRequestStateOpen && draft {
		return "bozza"
	}
	return string(state)
}

// check converte una risposta non-2xx nell'errore di gs.
func check(status int, body []byte) error { return repoenv.Check(status, body) }
