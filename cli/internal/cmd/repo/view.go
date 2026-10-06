package repo

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		web bool
	)
	cmd := &cobra.Command{
		Use:   "view [<owner>/<repo>]",
		Short: "Mostra un repository",
		Long: "Mostra nome, descrizione, visibilità, stato, branch principale e indirizzi di clone di un repo. " +
			"Senza argomento è il repo del remote origin (o -R). Con --web apre la pagina nel browser.",
		Example: "  gs repo view acme/web\n  gs repo view --json fullName,cloneUrls\n  gs repo view acme/web --web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un repo: gs repo view [<owner>/<repo>]")
			}
			owner, name, err := target(f, args)
			if err != nil {
				return err
			}
			if web && jo.Enabled() {
				return cmdutil.UsageErrorf("--web non si combina con --json né con --jq")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if web {
				u := e.webURL(owner, name)
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
				return openURL(u)
			}
			r, err := e.get(c.Context(), owner, name)
			if err != nil {
				return err
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, repoOut{Repository: *r, URL: e.webURL(owner, name)}, f.IO.OutTTY)
			}
			return e.renderRepo(r)
		},
	}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Apre il repo nel browser")
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}

func (e *env) renderRepo(r *gitstack.Repository) error {
	w := e.f.IO.Out
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("%s\n", r.FullName)
	if r.Description != "" {
		p("%s\n", r.Description)
	}
	state := "attivo"
	if r.Archived {
		state = "archiviato (sola lettura)"
	}
	p("Visibilità: %s\n", r.Visibility)
	p("Proprietario: %s (%s)\n", r.Owner.Name, r.Owner.Type)
	p("Stato: %s\n", state)
	if r.Empty {
		p("Contenuto: vuoto (nessun commit)\n")
	}
	p("Branch principale: %s\n", r.DefaultBranch)
	p("Protezione del branch principale: %s\n", map[bool]string{true: "attiva", false: "spenta"}[r.ProtectDefaultBranch])
	p("Clone HTTPS: %s\n", r.CloneUrls.Https)
	if r.CloneUrls.Ssh != nil {
		p("Clone SSH: %s\n", *r.CloneUrls.Ssh)
	}
	p("URL: %s\n", e.webURL(string(r.Owner.Name), r.Name))
	return nil
}
