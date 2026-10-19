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

func newMergeCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo           output.JSONOptions
		merge        bool
		squash       bool
		title        string
		message      string
		deleteBranch bool
		matchHead    string
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "merge <numero>",
		Short: "Unisci una Pull Request",
		Long: "Unisce la PR nel branch di destinazione (PR3). `--merge` (merge commit) o `--squash`; senza nessuno dei due " +
			"usa il metodo predefinito del repo. Il rebase non esiste. Le regole (bozza, approvazioni, stati obbligatori, conflitti) " +
			"le applica il server: `gs` mostra il motivo del rifiuto (409). L'unione è definitiva. Chiede conferma (G8): " +
			"`--yes` procede senza chiedere; senza terminale e senza `--yes` esce con 2 e non unisce.",
		Example: "  gs pr merge 7 --squash --yes\n  gs pr merge 7 --merge --delete-branch --match-head-commit <sha>\n  gs pr merge 7",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			if merge && squash {
				return cmdutil.UsageErrorf("--merge e --squash sono alternativi")
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Stai per unire la PR #%d di %s/%s: l'unione è definitiva.", n, e.Owner, e.Repo)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, ""); err != nil {
				return err
			}
			var method gitstack.MergeMethod
			switch {
			case squash:
				method = gitstack.Squash
			case merge:
				method = gitstack.Merge
			default:
				mr, err := e.Gen.GetPullRequestMergeabilityWithResponse(c.Context(), e.Owner, e.Repo, n)
				if err != nil {
					return err
				}
				if err := check(mr.StatusCode(), mr.Body); err != nil {
					return err
				}
				if mr.JSON200 == nil {
					return unexpected(mr.StatusCode())
				}
				method = mr.JSON200.DefaultMethod
			}
			in := gitstack.MergePullRequestInput{Method: method}
			if t := strings.TrimSpace(title); t != "" {
				in.CommitTitle = &t
			}
			if m := strings.TrimSpace(message); m != "" {
				in.CommitMessage = &m
			}
			if deleteBranch {
				d := true
				in.DeleteBranch = &d
			}
			if h := strings.TrimSpace(matchHead); h != "" {
				in.ExpectedHeadSha = &h
			}
			resp, err := e.Gen.MergePullRequestWithResponse(c.Context(), e.Owner, e.Repo, n, in)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if !jo.Enabled() && f.IO.OutTTY {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "PR #%d unita con %s\n", n, method)
			}
			return printPR(f, &jo, e, resp.JSON200)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&merge, "merge", false, "Metodo: merge commit")
	fl.BoolVar(&squash, "squash", false, "Metodo: squash")
	fl.StringVarP(&title, "title", "t", "", "Titolo del commit di merge o di squash (predefinito lo propone il server)")
	fl.StringVarP(&message, "message", "m", "", "Testo del commit di merge o di squash")
	fl.BoolVar(&deleteBranch, "delete-branch", false, "Elimina il ramo di origine dopo il merge")
	fl.StringVar(&matchHead, "match-head-commit", "", "SHA atteso della punta del ramo di origine: se è cambiata, 409 head_changed")
	fl.BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}
