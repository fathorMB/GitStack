package issue

import (
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newLockCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo     output.JSONOptions
		reason string
	)
	cmd := &cobra.Command{
		Use:   "lock <numero>",
		Short: "Blocca la discussione di una issue",
		Long: "Blocca la discussione (I11): da quel momento commenta solo chi ha write. Serve il permesso admin. " +
			"Bloccare una issue già bloccata non dà errore.",
		Example: "  gs issue lock 12 --reason \"discussione fuori tema\"",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			in := gitstack.LockIssueInput{}
			if reason != "" {
				in.Reason = &reason
			}
			resp, err := e.gen.LockIssueWithResponse(c.Context(), e.owner, e.repo, n, in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printIssue(&jo, resp.JSON200)
		},
	}
	cmd.Flags().StringVarP(&reason, "reason", "r", "", "Motivo, mostrato nella cronologia (massimo 256 caratteri)")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}

func newUnlockCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "unlock <numero>",
		Short:   "Sblocca la discussione di una issue",
		Long:    "Sblocca la discussione (I11). Serve il permesso admin. Sbloccare una issue non bloccata non dà errore.",
		Example: "  gs issue unlock 12",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.UnlockIssueWithResponse(c.Context(), e.owner, e.repo, n)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printIssue(&jo, resp.JSON200)
		},
	}
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}
