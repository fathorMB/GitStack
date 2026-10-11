// Package browse è il comando `gs browse` (G4): apre nel browser la UI sul
// repo, su un file (con riga), su una issue o sulle pagine di storico e
// blame. Con --no-browser stampa solo l'indirizzo.
package browse

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

// Target è la pagina da aprire. Un solo campo tra Issue, Issues e Path vale
// alla volta; senza nessuno è la pagina del repo.
type Target struct {
	Issue   int64  // numero di una issue
	Issues  bool   // elenco delle issue
	Path    string // file o cartella (una cartella finisce con `/`)
	Line    int    // riga di Path (0 = nessuna)
	Ref     string // branch, tag o sha; vuoto = quello predefinito del repo
	History bool   // storico dei commit (di Path, o del repo)
	Blame   bool   // blame di Path
}

// ParseArg legge l'argomento di `gs browse`: `12` o `#12` è una issue,
// `percorso[:riga]` è un file. Una riga si scrive dopo l'ultimo `:`.
func ParseArg(arg string) (Target, error) {
	s := strings.TrimSpace(arg)
	if s == "" {
		return Target{}, nil
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64); err == nil {
		if n < 1 {
			return Target{}, cmdutil.UsageErrorf("numero di issue non valido: %q", arg)
		}
		return Target{Issue: n}, nil
	}
	t := Target{Path: s}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			if n < 1 {
				return Target{}, cmdutil.UsageErrorf("numero di riga non valido: %q", arg)
			}
			t.Path, t.Line = s[:i], n
		}
	}
	t.Path = strings.TrimPrefix(strings.ReplaceAll(t.Path, "\\", "/"), "./")
	if t.Path == "." {
		t.Path = ""
	}
	if t.Path == "" && t.Line > 0 {
		return Target{}, cmdutil.UsageErrorf("la riga serve un file: %q", arg)
	}
	return t, nil
}

// NeedsRef dice se per costruire l'indirizzo serve il ref predefinito del
// repo (una chiamata all'API) perché l'utente non l'ha indicato.
func (t Target) NeedsRef() bool {
	if t.Ref != "" || t.Issue > 0 || t.Issues {
		return false
	}
	return t.Path != "" || t.History || t.Blame
}

// URL costruisce l'indirizzo della pagina web. base è l'origine della UI
// (`https://host`), senza `/` finale. Le pagine sono quelle di web/src/App.tsx.
func URL(base, owner, repo string, t Target) (string, error) {
	root := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(base, "/"), owner, repo)
	switch {
	case t.Issue > 0:
		return fmt.Sprintf("%s/issues/%d", root, t.Issue), nil
	case t.Issues:
		return root + "/issues", nil
	}
	ref := repoenv.EscapePath(t.Ref)
	path := repoenv.EscapePath(strings.Trim(t.Path, "/"))
	dir := strings.HasSuffix(t.Path, "/")
	switch {
	case t.Blame:
		if t.Path == "" || dir {
			return "", cmdutil.UsageErrorf("--blame vuole un file")
		}
		return fmt.Sprintf("%s/blame/%s/%s%s", root, ref, path, lineFrag(t.Line)), nil
	case t.History:
		if t.Line > 0 {
			return "", cmdutil.UsageErrorf("--history non ha una riga")
		}
		if t.Path == "" {
			return fmt.Sprintf("%s/commits/%s", root, ref), nil
		}
		return fmt.Sprintf("%s/commits/%s/%s", root, ref, path), nil
	case t.Path == "":
		if t.Ref == "" {
			return root, nil
		}
		return fmt.Sprintf("%s/tree/%s", root, ref), nil
	case dir:
		return fmt.Sprintf("%s/tree/%s/%s", root, ref, path), nil
	}
	return fmt.Sprintf("%s/blob/%s/%s%s", root, ref, path, lineFrag(t.Line)), nil
}

func lineFrag(n int) string {
	if n > 0 {
		return fmt.Sprintf("#L%d", n)
	}
	return ""
}

// NewCmd restituisce `gs browse`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		branch    string
		history   bool
		blame     bool
		issues    bool
		noBrowser bool
	)
	cmd := &cobra.Command{
		Use:   "browse [percorso[:riga] | numero]",
		Short: "Apri il repo, un file o una issue nel browser",
		Long: "Apre nel browser la pagina della UI del repo (`-R owner/repo` o remote origin). Senza argomenti è la pagina del repo; " +
			"`percorso` è un file (`percorso/` una cartella) e `:riga` lo apre su quella riga; un numero (`12` o `#12`) è una issue. " +
			"Il ref è il branch principale salvo --branch.\n\n" +
			"--history apre lo storico dei commit (del file se c'è un percorso, altrimenti del repo; per leggerlo nel terminale: `gs blame --history <file>`), " +
			"--blame il blame di un file, --issues l'elenco delle issue. Con --no-browser stampa soltanto l'indirizzo.",
		Example: "  gs browse\n" +
			"  gs browse cli/internal/cmd/browse/browse.go:42 --branch feature/uno\n" +
			"  gs browse --history README.md --no-browser\n" +
			"  gs browse 12",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("un solo argomento: %s [percorso[:riga] | numero]", c.CommandPath())
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			t := Target{}
			if len(args) == 1 {
				var err error
				if t, err = ParseArg(args[0]); err != nil {
					return err
				}
			}
			t.Ref, t.History, t.Blame, t.Issues = branch, history, blame, issues
			if n := b2i(history) + b2i(blame) + b2i(issues); n > 1 {
				return cmdutil.UsageErrorf("--history, --blame e --issues sono alternativi")
			}
			if t.Issue > 0 && (history || blame || issues || branch != "") {
				return cmdutil.UsageErrorf("una issue non si apre con --history, --blame, --issues o --branch")
			}
			if issues && (t.Path != "" || branch != "") {
				return cmdutil.UsageErrorf("--issues non vuole un percorso né --branch")
			}
			owner, repo, err := f.BaseRepo()
			if err != nil {
				return err
			}
			if _, err := URL("x", owner, repo, withRef(t)); err != nil {
				return err // uso errato: prima della rete
			}
			host, err := f.Host()
			if err != nil {
				return err
			}
			if t.NeedsRef() {
				gen, _, err := repoenv.Client(f)
				if err != nil {
					return err
				}
				resp, err := gen.GetRepositoryWithResponse(c.Context(), owner, repo)
				if err != nil {
					return err
				}
				if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return repoenv.Unexpected(resp.StatusCode())
				}
				t.Ref = defaultBranch(resp.JSON200)
			}
			u, err := URL(repoenv.WebBase(host), owner, repo, t)
			if err != nil {
				return err
			}
			if noBrowser {
				_, err := fmt.Fprintln(f.IO.Out, u)
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
			if err := f.OpenBrowser(u); err != nil {
				return fmt.Errorf("impossibile aprire il browser (usa --no-browser per l'indirizzo): %w", err)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&branch, "branch", "b", "", "Branch, tag o sha (default: branch principale)")
	fl.BoolVar(&history, "history", false, "Apri lo storico dei commit (del file, se c'è un percorso)")
	fl.BoolVar(&blame, "blame", false, "Apri il blame del file")
	fl.BoolVar(&issues, "issues", false, "Apri l'elenco delle issue")
	fl.BoolVarP(&noBrowser, "no-browser", "n", false, "Stampa l'indirizzo senza aprire il browser")
	return cmd
}

func defaultBranch(r *gitstack.Repository) string { return r.DefaultBranch }

// withRef dà un ref fittizio a un target che ne ha bisogno, per validare gli
// argomenti senza rete.
func withRef(t Target) Target {
	if t.Ref == "" {
		t.Ref = "main"
	}
	return t
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
