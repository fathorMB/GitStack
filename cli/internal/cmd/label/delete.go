package label

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

func newDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <nome>",
		Aliases: []string{"rm"},
		Short:   "Elimina un'etichetta",
		Long: "Elimina un'etichetta dal repo. Le issue che la usano restano, senza etichetta. " +
			"Serve il permesso write. Chiede di riscrivere il nome dell'etichetta (G8): senza " +
			"terminale serve --yes, altrimenti exit 2.",
		Example: "  gs label delete bug\n  gs label delete bug --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return cmdutil.UsageErrorf("il nome non può essere vuoto")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if err := confirmDelete(f, yes, name); err != nil {
				return err
			}
			resp, err := e.gen.DeleteLabelWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.LabelNameParam(name))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Eliminata etichetta %s\n", name)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}
