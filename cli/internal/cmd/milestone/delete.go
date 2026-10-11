package milestone

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

func newDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <numero>",
		Aliases: []string{"rm"},
		Short:   "Elimina una milestone",
		Long: "Elimina una milestone dal repo (I7). Le issue collegate restano, senza milestone. " +
			"Serve il permesso write. Chiede di riscrivere il numero della milestone (G8): " +
			"senza terminale serve --yes, altrimenti exit 2.",
		Example: "  gs milestone delete 3\n  gs milestone delete 3 --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if err := confirmDelete(f, yes, n); err != nil {
				return err
			}
			resp, err := e.gen.DeleteMilestoneWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.MilestoneNumberParam(n))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Eliminata milestone #%d\n", n)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}
