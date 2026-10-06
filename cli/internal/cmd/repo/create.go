package repo

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo          output.JSONOptions
		owner       string
		description string
		visibility  string
		private     bool
		internal    bool
		readme      bool
		gitignore   string
		license     string
		noLabels    bool
	)
	cmd := &cobra.Command{
		Use:   "create [<owner>/]<nome>",
		Short: "Crea un repository",
		Long: "Crea un repository. Il proprietario è l'utente che chiama, oppure l'organizzazione data con `<org>/<nome>` o --owner " +
			"(serve il permesso di crearne). Il repo è privato di default (P7): `--internal` lo apre a tutti gli utenti " +
			"dell'installazione; non esiste un accesso anonimo.\n\n" +
			"Il contenuto iniziale (R5) è facoltativo e spento di default: --add-readme, --gitignore <modello>, --license <modello> " +
			"fanno il primo commit su `main`. Sono create anche le etichette predefinite (I5), salvo --no-default-labels.\n\n" +
			"Su stdout l'indirizzo web del repo.",
		Example: "  gs repo create my-app\n  gs repo create acme/web --internal -d \"Il sito\"\n" +
			"  gs repo create tool --add-readme --gitignore go --license mit",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmdutil.UsageErrorf("serve il nome: gs repo create [<owner>/]<nome>")
			}
			name := strings.TrimSpace(args[0])
			if i := strings.Index(name, "/"); i >= 0 {
				o := name[:i]
				name = name[i+1:]
				if owner != "" && owner != o {
					return cmdutil.UsageErrorf("proprietario doppio: %q nel nome e %q in --owner", o, owner)
				}
				owner = o
			}
			if name == "" || strings.Contains(name, "/") {
				return cmdutil.UsageErrorf("nome del repo non valido: %q (atteso `[<owner>/]<nome>`)", args[0])
			}
			vis := gitstack.RepoVisibilityPrivate
			picked := 0
			if c.Flags().Changed("visibility") {
				picked++
				switch strings.ToLower(visibility) {
				case "private":
				case "internal":
					vis = gitstack.RepoVisibilityInternal
				default:
					return cmdutil.UsageErrorf("--visibility: private o internal (non %q; un repo non è mai pubblico)", visibility)
				}
			}
			if private {
				picked++
			}
			if internal {
				picked++
				vis = gitstack.RepoVisibilityInternal
			}
			if picked > 1 {
				return cmdutil.UsageErrorf("--visibility, --private e --internal sono alternativi")
			}
			in := gitstack.CreateRepositoryInput{Name: name, Visibility: &vis}
			if c.Flags().Changed("description") {
				in.Description = &description
			}
			if readme {
				in.Readme = &readme
			}
			if gitignore != "" {
				g := gitstack.GitignoreTemplate(strings.ToLower(gitignore))
				in.GitignoreTemplate = &g
			}
			if license != "" {
				l := gitstack.LicenseTemplate(strings.ToLower(license))
				in.LicenseTemplate = &l
			}
			if noLabels {
				no := false
				in.DefaultLabels = &no
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if owner == "" {
				if owner, err = e.me(c.Context()); err != nil {
					return err
				}
			}
			in.Owner = owner
			resp, err := e.gen.CreateRepositoryWithResponse(c.Context(), in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printRepo(&jo, resp.JSON201)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&owner, "owner", "o", "", "Utente o organizzazione proprietaria (predefinito: tu)")
	fl.StringVarP(&description, "description", "d", "", "Descrizione")
	fl.StringVar(&visibility, "visibility", "private", "Visibilità: private (predefinita) o internal")
	fl.BoolVar(&private, "private", false, "Repo privato (è già il predefinito)")
	fl.BoolVar(&internal, "internal", false, "Repo interno: leggibile da tutti gli utenti dell'installazione")
	fl.BoolVar(&readme, "add-readme", false, "Crea un README.md iniziale")
	fl.StringVar(&gitignore, "gitignore", "", "Modello di .gitignore iniziale (go, node, python, ...)")
	fl.StringVar(&license, "license", "", "Modello di licenza iniziale (mit, apache-2.0, ...)")
	fl.BoolVar(&noLabels, "no-default-labels", false, "Non creare le etichette predefinite")
	output.AddJSONFlags(cmd, &jo, RepoFields)
	return cmd
}

// printRepo stampa un repo: JSON se richiesto, altrimenti l'indirizzo web.
func (e *env) printRepo(jo *output.JSONOptions, r *gitstack.Repository) error {
	if jo.Enabled() {
		return jo.Write(e.f.IO.Out, repoOut{Repository: *r, URL: e.webURL(string(r.Owner.Name), r.Name)}, e.f.IO.OutTTY)
	}
	_, err := fmt.Fprintln(e.f.IO.Out, e.webURL(string(r.Owner.Name), r.Name))
	return err
}
