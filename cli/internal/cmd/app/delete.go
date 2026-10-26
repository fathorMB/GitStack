package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	gitstack "github.com/fathorMB/GitStack/client/go"
)

// newDeleteCmd è `gs app delete`: operazione distruttiva (H5, v3-b). Elimina l'app e, con lei, i
// volumi e i loro dati. Chiede sempre la conferma (G8): si riscrive il nome dell'app, oppure --yes
// senza terminale. Il nome confermato viaggia come `confirm`, che l'API pretende se l'app ha volumi.
func newDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <app>",
		Aliases: []string{"rm"},
		Short:   "Elimina un'app e i suoi volumi",
		Long: "Elimina l'app con versioni, variabili, segreti e indirizzi. I volumi persistenti e i loro dati vengono " +
			"eliminati con l'app e lo spazio torna libero nella quota dell'owner: non si annulla. Chiede la conferma " +
			"(G8): riscrivi il nome dell'app, o --yes senza terminale. Serve `admin` sul repo.",
		Example: "  gs app delete web --repo acme/handbook\n  gs app delete web --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Eliminare %s con i suoi volumi e i dati. Non si annulla. Riscrivi `%s` per confermare:", app, app)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, app); err != nil {
				return err
			}
			resp, err := e.gen.DeleteAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), &gitstack.DeleteAppParams{Confirm: &app})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.Out, "deleted %s\n", app)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}
