package db

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

type appRef struct{ owner, repo, name string }

func (a appRef) String() string { return a.owner + "/" + a.repo + "/" + a.name }

// parseApp legge owner/repo/app.
func parseApp(s string) (appRef, error) {
	p := strings.Split(strings.TrimSpace(s), "/")
	if len(p) != 3 || p[0] == "" || p[1] == "" || p[2] == "" {
		return appRef{}, cmdutil.UsageErrorf("--app deve essere owner/repo/app, ricevuto %q", s)
	}
	return appRef{p[0], p[1], p[2]}, nil
}

func newLinkCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo   output.JSONOptions
		ow   ownerFlag
		app  string
		mode string
		list bool
	)
	cmd := &cobra.Command{
		Use:   "link <db> --app <owner/repo/app> [--mode rw|ro]",
		Short: "Collega un'app al database (o elenca i collegamenti con --list)",
		Long: "Collega l'app al database con un ruolo di login proprio: `rw` (predefinito) legge e scrive, `ro` solo legge. " +
			"App e database devono avere lo stesso owner. L'app riceve DATABASE_URL e le variabili PG* come segreti e riavvia; " +
			"se ha già una variabile con uno di quei nomi il collegamento è rifiutato (`variable_conflict`). La password del " +
			"collegamento si vede solo ora. Serve `admin` sull'app e sull'owner. Con --list mostra i collegamenti esistenti.",
		Example: "  gs db link acme/shop --app acme/web/api\n  gs db link shop --owner acme --app acme/web/report --mode ro\n  gs db link acme/shop --list",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			if list {
				return listLinks(c, f, owner, name, &jo)
			}
			if app == "" {
				return cmdutil.UsageErrorf("serve --app owner/repo/app")
			}
			a, err := parseApp(app)
			if err != nil {
				return err
			}
			if mode != "rw" && mode != "ro" {
				return cmdutil.UsageErrorf("--mode deve essere rw o ro")
			}
			var in gitstack.DatabaseLinkCreateInput
			in.App.Owner, in.App.Repo, in.App.Name = a.owner, a.repo, a.name
			in.Mode = gitstack.DatabaseLinkMode(mode)
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.CreateDatabaseLinkWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, resp.JSON201, f.IO.OutTTY)
			}
			cr := resp.JSON201.Credentials
			_, _ = fmt.Fprintf(f.IO.Out, "collegato %s a %s/%s (%s)\n", a, owner, name, resp.JSON201.Link.Mode)
			_, _ = fmt.Fprintf(f.IO.Out, "utente: %s\npassword: %s\nhost: %s\nporta: %d\ndatabase: %s\nsslmode: %s\n", cr.Username, cr.Password, cr.Host, cr.Port, cr.Database, cr.SslMode)
			_, _ = fmt.Fprintln(f.IO.ErrOut, "La password si vede solo ora; l'app la riceve già come segreto.")
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().StringVar(&app, "app", "", "App da collegare, `owner/repo/app`")
	cmd.Flags().StringVar(&mode, "mode", "rw", "Modalità: rw (legge e scrive) o ro (sola lettura)")
	cmd.Flags().BoolVar(&list, "list", false, "Elenca i collegamenti del database invece di crearne uno")
	output.AddJSONFlags(cmd, &jo, []string{"id", "app", "mode", "state", "createdAt", "createdBy", "link", "credentials"})
	return cmd
}

func listLinks(c *cobra.Command, f *cmdutil.Factory, owner, name string, jo *output.JSONOptions) error {
	e, err := newEnv(f)
	if err != nil {
		return err
	}
	resp, err := e.gen.ListDatabaseLinksWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
	if err != nil {
		return err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return unexpected(resp.StatusCode())
	}
	if jo.Enabled() {
		return jo.Write(f.IO.Out, resp.JSON200.Items, f.IO.OutTTY)
	}
	if len(resp.JSON200.Items) == 0 {
		if f.IO.OutTTY {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "%s/%s non ha collegamenti\n", owner, name)
		}
		return nil
	}
	t := output.NewTable(f.IO.OutTTY, f.IO.Width, "APP", "MODO", "STATO", "CREATO")
	for _, l := range resp.JSON200.Items {
		t.AddRow(appRef{l.App.Owner, l.App.Repo, l.App.Name}.String(), string(l.Mode), string(l.State), day(l.CreatedAt))
	}
	return t.Render(f.IO.Out)
}

func newUnlinkCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		ow  ownerFlag
		app string
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "unlink <db> --app <owner/repo/app>",
		Short: "Scollega un'app dal database",
		Long: "Chiude il ruolo di login del collegamento (le sessioni aperte terminano) e toglie le variabili dall'app, che riavvia. " +
			"Finché il ruolo esiste il collegamento resta `unlinking`. Chiede la conferma (G8): riscrivi l'app, o --yes senza " +
			"terminale. Serve `admin` sull'app o sull'owner.",
		Example: "  gs db unlink acme/shop --app acme/web/api --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			if app == "" {
				return cmdutil.UsageErrorf("serve --app owner/repo/app")
			}
			a, err := parseApp(app)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			lr, err := e.gen.ListDatabaseLinksWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
			if err != nil {
				return err
			}
			if err := e.check(lr.StatusCode(), lr.Body); err != nil {
				return err
			}
			if lr.JSON200 == nil {
				return unexpected(lr.StatusCode())
			}
			var found *gitstack.DatabaseLink
			for i, l := range lr.JSON200.Items {
				if (appRef{l.App.Owner, l.App.Repo, l.App.Name}) == a {
					found = &lr.JSON200.Items[i]
					break
				}
			}
			if found == nil {
				return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, ErrCode: "not_found", Err: fmt.Errorf("%s non è collegata a %s/%s", a, owner, name)}
			}
			prompt := fmt.Sprintf("Scollegare %s da %s/%s: l'app perde le variabili e riavvia. Riscrivi `%s` per confermare:", a, owner, name, a)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, a.String()); err != nil {
				return err
			}
			resp, err := e.gen.DeleteDatabaseLinkWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, found.Id)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if jo.Enabled() && resp.JSON202 != nil {
				return jo.Write(f.IO.Out, resp.JSON202, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "scollegamento di %s da %s/%s avviato\n", a, owner, name)
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().StringVar(&app, "app", "", "App da scollegare, `owner/repo/app`")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, []string{"id", "app", "mode", "state", "createdAt", "createdBy"})
	return cmd
}
