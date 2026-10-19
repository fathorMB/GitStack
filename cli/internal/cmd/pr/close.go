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

func newCloseCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo      output.JSONOptions
		comment string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "close <numero>",
		Short: "Chiudi una Pull Request senza unirla",
		Long: "Chiude la PR senza unirla (PR6): il ramo di origine resta, e la PR si può riaprire finché il ramo esiste. " +
			"Possono chiudere l'autore e chi ha `write`. Chiede conferma (G8): `--yes` procede senza chiedere; senza terminale " +
			"e senza `--yes` esce con 2 e non chiude. `--comment` lascia un commento dopo la chiusura.",
		Example: "  gs pr close 7 --yes\n  gs pr close 7 --comment \"Sostituita dalla #9\" --yes",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Stai per chiudere la PR #%d di %s/%s senza unirla.", n, e.Owner, e.Repo)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, ""); err != nil {
				return err
			}
			resp, err := e.Gen.ClosePullRequestWithResponse(c.Context(), e.Owner, e.Repo, n)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if text := strings.TrimSpace(comment); text != "" {
				cr, err := e.Gen.CreatePullRequestCommentWithResponse(c.Context(), e.Owner, e.Repo, n, gitstack.CreatePullRequestCommentInput{Body: text})
				if err != nil {
					return err
				}
				if err := check(cr.StatusCode(), cr.Body); err != nil {
					return fmt.Errorf("PR #%d chiusa, ma il commento non è stato scritto: %w", n, err)
				}
			}
			if !jo.Enabled() && f.IO.OutTTY {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "PR #%d chiusa\n", n)
			}
			return printPR(f, &jo, e, resp.JSON200)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&comment, "comment", "c", "", "Lascia un commento dopo la chiusura")
	fl.BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}
