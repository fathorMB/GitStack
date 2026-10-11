package repo

import (
	"fmt"
	"strings"

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
		Short:   "Elimina un repository (recuperabile per 7 giorni)",
		Long: "Elimina un repo (R12, R2): sparisce subito da elenchi e clone, ma per 7 giorni si recupera con `gs repo restore`; " +
			"poi è cancellato per sempre. Serve il ruolo admin. Chiede di riscrivere `owner/repo` (G8): senza terminale serve --yes, altrimenti exit 2.",
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
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Eliminato %s/%s: si recupera per 7 giorni con `gs repo restore %s/%s`\n", owner, name, owner, name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

func newRestoreCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "restore <owner>/<repo>",
		Short: "Recupera un repository eliminato",
		Long: "Recupera un repo eliminato da meno di 7 giorni (R2) con il suo contenuto. Il nome non deve essere stato preso nel frattempo " +
			"(409). Si trovano con `gs repo list --deleted`. Su stdout l'indirizzo web.",
		Example: "  gs repo restore acme/web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmdutil.UsageErrorf("serve il repo: gs repo restore <owner>/<repo>")
			}
			owner, name, err := parseTarget(args[0])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			lr, err := e.gen.ListDeletedRepositoriesWithResponse(c.Context(), &gitstack.ListDeletedRepositoriesParams{Owner: &owner})
			if err != nil {
				return err
			}
			if err := e.check(lr.StatusCode(), lr.Body); err != nil {
				return err
			}
			if lr.JSON200 == nil {
				return unexpected(lr.StatusCode())
			}
			var found *gitstack.DeletedRepository
			for i, d := range lr.JSON200.Items {
				if strings.EqualFold(string(d.Owner.Name), owner) && d.Name == name {
					found = &lr.JSON200.Items[i]
					break
				}
			}
			if found == nil {
				return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, ErrCode: "not_found",
					Err: fmt.Errorf("nessun repository eliminato %s/%s da recuperare (si vedono con `gs repo list --deleted`)", owner, name)}
			}
			resp, err := e.gen.RestoreRepositoryWithResponse(c.Context(), found.Id)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if !jo.Enabled() {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Recuperato %s\n", resp.JSON200.FullName)
			}
			return e.printRepo(&jo, resp.JSON200)
		},
	}
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}
