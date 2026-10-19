package pr

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		web bool
	)
	cmd := &cobra.Command{
		Use:   "view <numero>",
		Short: "Mostra una Pull Request",
		Long: "Mostra titolo, stato (bozza, aperta, chiusa, unita), rami, autore, revisori richiesti, decisione di review " +
			"e testo. Con --web apre la pagina nel browser. Conflitti e blocchi al merge: `gs api /repos/<owner>/<repo>/pulls/<n>/mergeability`.",
		Example: "  gs pr view 7\n  gs pr view #7 --json title,state,reviewDecision\n  gs pr view 7 --web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			if web && jo.Enabled() {
				return cmdutil.UsageErrorf("--web non si combina con --json né con --jq")
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			if web {
				u := webURL(e, n)
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
				return f.OpenBrowser(u)
			}
			resp, err := e.Gen.GetPullRequestWithResponse(c.Context(), e.Owner, e.Repo, n)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, prOut{PullRequest: *resp.JSON200, URL: webURL(e, n)}, f.IO.OutTTY)
			}
			return renderPR(f, e, resp.JSON200)
		},
	}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Apre la PR nel browser")
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}

// renderPR stampa una PR in testo: intestazione, metadati, testo.
func renderPR(f *cmdutil.Factory, e *repoenv.Env, p *gitstack.PullRequest) error {
	w := f.IO.Out
	_, _ = fmt.Fprintf(w, "#%d %s\n", p.Number, p.Title)
	_, _ = fmt.Fprintf(w, "stato:    %s\n", stateLabel(p.State, p.Draft))
	_, _ = fmt.Fprintf(w, "autore:   %s\n", p.Author.Username)
	_, _ = fmt.Fprintf(w, "rami:     %s -> %s\n", p.SourceBranch, p.TargetBranch)
	var rs []string
	for _, r := range p.RequestedReviewers {
		rs = append(rs, r.Username)
	}
	if len(rs) == 0 {
		rs = []string{"nessuno"}
	}
	_, _ = fmt.Fprintf(w, "revisori: %s\n", strings.Join(rs, ", "))
	_, _ = fmt.Fprintf(w, "review:   %s (approvazioni %d di %d richieste)\n", p.ReviewDecision, p.Approvals, p.RequiredApprovals)
	if len(p.ClosingIssues) > 0 {
		var is []string
		for _, n := range p.ClosingIssues {
			is = append(is, fmt.Sprintf("#%d", n))
		}
		_, _ = fmt.Fprintf(w, "chiude:   %s al merge\n", strings.Join(is, ", "))
	}
	_, _ = fmt.Fprintf(w, "commit %d, file %d (+%d -%d)\n", p.CommitCount, p.ChangedFiles, p.Additions, p.Deletions)
	_, _ = fmt.Fprintf(w, "%s\n", webURL(e, p.Number))
	if strings.TrimSpace(p.Body) != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", strings.TrimRight(p.Body, "\n"))
	}
	return nil
}
