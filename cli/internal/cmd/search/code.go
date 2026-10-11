package search

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

// CodeFields sono i campi di --json di `gs search code`.
var CodeFields = []string{"query", "ref", "limitReached", "timedOut", "results"}

// MaxCodeResults è il tetto dei risultati di B5, imposto dal server.
const MaxCodeResults = 100

type codeHit struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Fragment string `json:"fragment"`
	URL      string `json:"url"`
}

type codeOut struct {
	Query        string    `json:"query"`
	Ref          string    `json:"ref"`
	LimitReached bool      `json:"limitReached"`
	TimedOut     bool      `json:"timedOut"`
	Results      []codeHit `json:"results"`
}

func newCodeCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		ref string
	)
	cmd := &cobra.Command{
		Use:   "code <testo>",
		Short: "Cerca un testo nel codice di un repo",
		Long: "Cerca un testo (sottostringa letterale, senza distinguere maiuscole, da 2 a 256 caratteri) nei file di testo del repo, " +
			"senza indice (B5). Il repo è quello di `-R owner/repo` o del remote origin; il ref è il branch principale salvo --ref.\n\n" +
			"I risultati sono al massimo 100 e la ricerca si interrompe dopo 10 secondi: in entrambi i casi gs lo dice su stderr " +
			"(e `limitReached` / `timedOut` valgono true in --json), perché l'elenco è incompleto.",
		Example: "  gs search code 'func main' -R acme/web\n" +
			"  gs search code TODO --ref feature/uno --json results,limitReached",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			q := strings.Join(args, " ")
			if n := utf8.RuneCountInString(q); n < 2 || n > 256 {
				return cmdutil.UsageErrorf("il testo da cercare deve avere da 2 a 256 caratteri: gs search code <testo>")
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			p := gitstack.SearchRepositoryCodeParams{Q: q}
			if ref != "" {
				p.Ref = &ref
			}
			resp, err := e.Gen.SearchRepositoryCodeWithResponse(c.Context(), e.Owner, e.Repo, &p)
			if err != nil {
				return err
			}
			if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			r := resp.JSON200
			if r == nil {
				return repoenv.Unexpected(resp.StatusCode())
			}
			web := fmt.Sprintf("%s/%s/%s", repoenv.WebBase(e.Host), e.Owner, e.Repo)
			hits := make([]codeHit, len(r.Results))
			for i, h := range r.Results {
				hits[i] = codeHit{Path: h.Path, Line: h.Line, Fragment: h.Fragment,
					URL: fmt.Sprintf("%s/blob/%s/%s#L%d", web, repoenv.EscapePath(r.Ref), repoenv.EscapePath(h.Path), h.Line)}
			}
			if jo.Enabled() {
				out := codeOut{Query: r.Query, Ref: r.Ref, LimitReached: r.LimitReached, TimedOut: r.TimedOut, Results: hits}
				if err := jo.Write(f.IO.Out, out, f.IO.OutTTY); err != nil {
					return err
				}
			} else {
				for _, h := range hits {
					_, _ = fmt.Fprintf(f.IO.Out, "%s:%d: %s\n", h.Path, h.Line, strings.TrimSpace(h.Fragment))
				}
				if len(hits) == 0 && f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessun risultato per %q in %s/%s (%s)\n", q, e.Owner, e.Repo, r.Ref)
				}
			}
			if r.LimitReached {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Risultati limitati ai primi %d: ce ne sono altri. Restringi la ricerca (testo più specifico o un altro --ref)\n", MaxCodeResults)
			}
			if r.TimedOut {
				_, _ = fmt.Fprintln(f.IO.ErrOut, "Ricerca interrotta dopo 10 secondi: i risultati sono parziali")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "Branch, tag o sha (default: branch principale)")
	output.AddJSONFlags(cmd, &jo, CodeFields)
	return cmd
}
