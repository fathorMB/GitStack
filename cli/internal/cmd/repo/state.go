package repo

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// confirmName è la conferma G8 delle operazioni distruttive: riscrivere
// `owner/repo`. Con --yes procede; senza terminale e senza --yes è un uso
// errato (exit 2) e non si legge stdin.
func confirmName(f *cmdutil.Factory, yes bool, what, owner, name string) error {
	full := owner + "/" + name
	prompt := fmt.Sprintf("%s %s. Riscrivi `%s` per confermare:", what, full, full)
	return cmdutil.ConfirmOrYes(f.IO, yes, prompt, full)
}

func newArchiveCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "archive [<owner>/<repo>]",
		Short: "Archivia un repository (sola lettura)",
		Long: "Archivia un repo (R10): resta leggibile e clonabile, ma push, issue e commenti sono rifiutati (409 `archived`) finché " +
			"non lo riattivi con `gs repo unarchive`. Serve il ruolo admin. Chiede di riscrivere `owner/repo` (G8): senza terminale " +
			"serve --yes, altrimenti exit 2.",
		Example: "  gs repo archive acme/web\n  gs repo archive acme/web --yes",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un repo: gs repo archive [<owner>/<repo>]")
			}
			owner, name, err := target(f, args)
			if err != nil {
				return err
			}
			if err := confirmName(f, yes, "Stai per archiviare", owner, name); err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			t := true
			r, err := e.update(c, owner, name, gitstack.UpdateRepositoryInput{Archived: &t})
			if err != nil {
				return err
			}
			if !jo.Enabled() {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Archiviato %s\n", r.FullName)
			}
			return e.printRepo(&jo, r)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}

func newUnarchiveCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "unarchive [<owner>/<repo>]",
		Short:   "Riattiva un repository archiviato",
		Long:    "Toglie l'archiviazione (R10): push, issue e commenti tornano possibili. Serve il ruolo admin. Non chiede conferma: non è distruttivo.",
		Example: "  gs repo unarchive acme/web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un repo: gs repo unarchive [<owner>/<repo>]")
			}
			owner, name, err := target(f, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			no := false
			r, err := e.update(c, owner, name, gitstack.UpdateRepositoryInput{Archived: &no})
			if err != nil {
				return err
			}
			if !jo.Enabled() {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Riattivato %s\n", r.FullName)
			}
			return e.printRepo(&jo, r)
		},
	}
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}

func newDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete [<owner>/<repo>]",
		Aliases: []string{"rm"},
		Short:   "Elimina un repository, subito e per sempre",
		Long: "Elimina un repo (R12, R2): l'eliminazione è definitiva e immediata, senza cestino. Spariscono dati git, issue, PR, release, immagini, app e volumi, " +
			"e il nome torna libero; si recupera solo da un backup (`gitstack restore`). Serve il ruolo admin. " +
			"Chiede di riscrivere `owner/repo` (G8): senza terminale serve --yes, altrimenti exit 2.",
		Example: "  gs repo delete acme/web\n  gs repo delete acme/web --yes",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un repo: gs repo delete [<owner>/<repo>]")
			}
			owner, name, err := target(f, args)
			if err != nil {
				return err
			}
			if err := confirmName(f, yes, "Stai per eliminare", owner, name); err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.DeleteRepositoryWithResponse(c.Context(), owner, name)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Eliminato %s/%s: definitivo, si recupera solo da un backup (`gitstack restore`)\n", owner, name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}
