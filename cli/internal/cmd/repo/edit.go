package repo

import (
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newEditCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo            output.JSONOptions
		description   string
		visibility    string
		defaultBranch string
		protect       bool
	)
	cmd := &cobra.Command{
		Use:   "edit [<owner>/<repo>]",
		Short: "Modifica le impostazioni di un repository",
		Long: "Modifica descrizione, visibilità (private o internal, P7), branch principale e protezione del branch principale (R9). " +
			"Serve il ruolo admin sul repo. Per archiviare usa `gs repo archive`. Su stdout l'indirizzo web.",
		Example: "  gs repo edit acme/web -d \"Il sito\" --visibility internal\n  gs repo edit --default-branch develop --protect-default-branch=false",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un repo: gs repo edit [<owner>/<repo>]")
			}
			owner, name, err := target(f, args)
			if err != nil {
				return err
			}
			fl := c.Flags()
			in := gitstack.UpdateRepositoryInput{}
			changed := false
			if fl.Changed("description") {
				in.Description, changed = &description, true
			}
			if fl.Changed("visibility") {
				switch strings.ToLower(visibility) {
				case "private", "internal":
					v := gitstack.RepoVisibility(strings.ToLower(visibility))
					in.Visibility, changed = &v, true
				default:
					return cmdutil.UsageErrorf("--visibility: private o internal (non %q; un repo non è mai pubblico)", visibility)
				}
			}
			if fl.Changed("default-branch") {
				if strings.TrimSpace(defaultBranch) == "" {
					return cmdutil.UsageErrorf("--default-branch non può essere vuoto")
				}
				in.DefaultBranch, changed = &defaultBranch, true
			}
			if fl.Changed("protect-default-branch") {
				in.ProtectDefaultBranch, changed = &protect, true
			}
			if !changed {
				return cmdutil.UsageErrorf("niente da modificare: indica almeno un flag (--description, --visibility, --default-branch, --protect-default-branch)")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			r, err := e.update(c, owner, name, in)
			if err != nil {
				return err
			}
			return e.printRepo(&jo, r)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&description, "description", "d", "", "Nuova descrizione")
	fl.StringVar(&visibility, "visibility", "", "Visibilità: private o internal")
	fl.StringVar(&defaultBranch, "default-branch", "", "Branch principale")
	fl.BoolVar(&protect, "protect-default-branch", false, "Rifiuta force-push ed eliminazione del branch principale (=false per spegnere)")
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}

func (e *env) update(c *cobra.Command, owner, name string, in gitstack.UpdateRepositoryInput) (*gitstack.Repository, error) {
	resp, err := e.gen.UpdateRepositoryWithResponse(c.Context(), owner, name, in)
	if err != nil {
		return nil, err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	return resp.JSON200, nil
}
