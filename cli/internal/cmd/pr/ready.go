package pr

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

func newReadyCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo        output.JSONOptions
		undo      bool
		reviewers []string
	)
	cmd := &cobra.Command{
		Use:   "ready <numero>",
		Short: "Segna una bozza come pronta per la review (Ready for review)",
		Long: "Toglie la bozza: da qui la PR si può unire e chiede review ai revisori indicati (PR6, PR11). " +
			"`--reviewer` chiede la review a chi ha affidato la issue, dopo averla resa pronta (una bozza non chiede review, 409 `draft`); " +
			"ripetibile, e rifare la richiesta a chi ha già recensito la riporta fra i revisori (PR12). " +
			"`--undo` riporta la PR in bozza. Serve `write` o essere l'autore.",
		Example: "  gs pr ready 7\n  gs pr ready 7 --reviewer alice --reviewer bob\n  gs pr ready 7 --undo",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			if undo && len(reviewers) > 0 {
				return cmdutil.UsageErrorf("--undo non si combina con --reviewer")
			}
			names := cleanNames(reviewers)
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			var final *gitstack.PullRequest
			if undo {
				resp, err := e.Gen.MarkPullRequestDraftWithResponse(c.Context(), e.Owner, e.Repo, n)
				if err != nil {
					return err
				}
				if err := check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				final = resp.JSON200
			} else {
				resp, err := e.Gen.MarkPullRequestReadyWithResponse(c.Context(), e.Owner, e.Repo, n)
				if err != nil {
					return err
				}
				if err := check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				final = resp.JSON200
				if len(names) > 0 {
					rr, err := e.Gen.RequestPullRequestReviewersWithResponse(c.Context(), e.Owner, e.Repo, n, gitstack.RequestReviewersInput{Reviewers: names})
					if err != nil {
						return err
					}
					if err := check(rr.StatusCode(), rr.Body); err != nil {
						return fmt.Errorf("PR #%d pronta, ma la richiesta di review non è riuscita: %w", n, err)
					}
					if rr.JSON200 == nil {
						return unexpected(rr.StatusCode())
					}
					final = rr.JSON200
				}
			}
			if !jo.Enabled() && f.IO.OutTTY {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "PR #%d: %s\n", n, stateLabel(final.State, final.Draft))
			}
			return printPR(f, &jo, e, final)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&undo, "undo", false, "Riporta la PR in bozza")
	fl.StringSliceVar(&reviewers, "reviewer", nil, "Chiede la review a questo utente (ripetibile)")
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}
