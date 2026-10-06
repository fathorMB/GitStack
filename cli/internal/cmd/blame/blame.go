// Package blame è il comando `gs blame` (G4, B4): chi ha scritto ogni riga di
// un file, con l'autore e il badge «agent», e con --history lo storico dei
// commit che toccano il file.
//
// Output JSON. Senza --history: oggetto con `path`, `ref`, `ranges` (ogni
// intervallo ha `startLine`, `endLine`, `commit`) e `url` (pagina blame della
// UI); l'autore di un commit porta `user.kind` («human» o «agent») se la sua
// email corrisponde a un utente GitStack. Con --history: lista di commit
// (CommitSummary: `sha`, `subject`, `message`, `author`, `committer`,
// `parents`) più `url` (pagina del commit). I campi sono in BlameFields e
// HistoryFields e documentati in cli/README.md.
package blame

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

// BlameFields sono i campi di --json di `gs blame`.
var BlameFields = []string{"path", "ref", "ranges", "url"}

// HistoryFields sono i campi di --json di `gs blame --history` (--json accetta
// l'unione dei due elenchi: ognuno vale per la sua modalità).
var HistoryFields = []string{"sha", "subject", "message", "author", "committer", "parents", "url"}

type blameOut struct {
	gitstack.Blame
	URL string `json:"url"`
}

type commitOut struct {
	gitstack.CommitSummary
	URL string `json:"url"`
}

// NewCmd restituisce `gs blame`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo      output.JSONOptions
		ref     string
		history bool
		author  string
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "blame <file>",
		Short: "Mostra chi ha modificato ogni riga di un file (o il suo storico)",
		Long: "Per ogni riga di <file> mostra il commit che l'ha scritta per ultima, con l'autore; " +
			"gli utenti agent portano il badge `[agent]` (B4). Il repo è quello di `-R owner/repo` o del remote origin; " +
			"il ref è il branch principale salvo --ref.\n\n" +
			"Un file binario o oltre 1 MB non ha il blame: gs esce con un errore che lo dice (HTTP 400, `blame_unavailable`).\n\n" +
			"Con --history non mostra le righe ma i commit che toccano il file, dal più recente (--limit, --author). " +
			"Per aprire lo storico nella UI usa `gs browse --history <file>`.",
		Example: "  gs blame cmd/main.go\n" +
			"  gs blame README.md --ref v1.0 --json ranges\n" +
			"  gs blame --history cmd/main.go -L 10 --author botty",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmdutil.UsageErrorf("serve il percorso del file: %s <file>", c.CommandPath())
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			path := strings.TrimPrefix(strings.ReplaceAll(args[0], "\\", "/"), "./")
			if path == "" || strings.HasSuffix(path, "/") {
				return cmdutil.UsageErrorf("percorso del file non valido: %q", args[0])
			}
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			if !history && (author != "" || c.Flags().Changed("limit")) {
				return cmdutil.UsageErrorf("--author e --limit valgono solo con --history")
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			web := fmt.Sprintf("%s/%s/%s", repoenv.WebBase(e.Host), e.Owner, e.Repo)
			if history {
				return runHistory(c, e, web, &jo, path, ref, author, limit)
			}
			return runBlame(c, e, web, &jo, path, ref)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&ref, "ref", "", "Branch, tag o sha (default: branch principale)")
	fl.BoolVar(&history, "history", false, "Elenca i commit che toccano il file invece del blame")
	fl.StringVar(&author, "author", "", "Con --history: solo i commit di questo autore (nome o email)")
	fl.IntVarP(&limit, "limit", "L", 30, "Con --history: numero massimo di commit")
	output.AddJSONFlags(cmd, &jo, append(append([]string{}, BlameFields...), HistoryFields...))
	return cmd
}

// who è l'autore di un commit: lo username se l'email corrisponde a un
// utente GitStack (con il badge se è un agent), altrimenti il nome nel commit.
func who(p gitstack.CommitPerson) string {
	if p.User != nil {
		s := string(p.User.Username)
		if p.User.Kind == gitstack.CodeUserKindAgent {
			s += " [agent]"
		}
		return s
	}
	return p.Name
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func runBlame(c *cobra.Command, e *repoenv.Env, web string, jo *output.JSONOptions, path, ref string) error {
	f := e.F
	bp := gitstack.GetRepositoryBlameParams{Path: path}
	if ref != "" {
		bp.Ref = &ref
	}
	resp, err := e.Gen.GetRepositoryBlameWithResponse(c.Context(), e.Owner, e.Repo, &bp)
	if err != nil {
		return err
	}
	if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
		var ae *cmdutil.APIError
		if errors.As(err, &ae) && ae.Code == "blame_unavailable" {
			ae.Message = fmt.Sprintf("blame non disponibile per %s: il file è binario o supera 1 MB (B4)", path)
		}
		return err
	}
	bl := resp.JSON200
	if bl == nil {
		return repoenv.Unexpected(resp.StatusCode())
	}
	url := fmt.Sprintf("%s/blame/%s/%s", web, repoenv.EscapePath(bl.Ref), repoenv.EscapePath(bl.Path))
	if jo.Enabled() {
		return jo.Write(f.IO.Out, blameOut{Blame: *bl, URL: url}, f.IO.OutTTY)
	}

	// il testo delle righe viene dal file, allo stesso ref del blame
	fp := gitstack.GetRepositoryFileParams{Path: path, Ref: &bl.Ref}
	fr, err := e.Gen.GetRepositoryFileWithResponse(c.Context(), e.Owner, e.Repo, &fp)
	if err != nil {
		return err
	}
	if err := repoenv.Check(fr.StatusCode(), fr.Body); err != nil {
		return err
	}
	var text []string
	if fr.JSON200 != nil && fr.JSON200.Content != nil {
		text = strings.Split(strings.TrimSuffix(*fr.JSON200.Content, "\n"), "\n")
	}
	width := 0
	for _, r := range bl.Ranges {
		if w := len(who(r.Commit.Author)); w > width {
			width = w
		}
	}
	for _, r := range bl.Ranges {
		for n := r.StartLine; n <= r.EndLine; n++ {
			line := ""
			if n-1 < len(text) {
				line = text[n-1]
			}
			_, _ = fmt.Fprintf(f.IO.Out, "%s %-*s %s %4d) %s\n", short(r.Commit.Sha), width, who(r.Commit.Author),
				r.Commit.Author.Date.UTC().Format(time.DateOnly), n, line)
		}
	}
	return nil
}

func runHistory(c *cobra.Command, e *repoenv.Env, web string, jo *output.JSONOptions, path, ref, author string, limit int) error {
	f := e.F
	p := gitstack.GetRepositoryCommitsParams{Path: &path}
	if ref != "" {
		p.Ref = &ref
	}
	if author != "" {
		p.Author = &author
	}
	per := limit
	if per > 100 {
		per = 100
	}
	p.PerPage = &per
	var items []gitstack.CommitSummary
	more := false
	for page := 1; len(items) < limit; page++ {
		pg := page
		p.Page = &pg
		resp, err := e.Gen.GetRepositoryCommitsWithResponse(c.Context(), e.Owner, e.Repo, &p)
		if err != nil {
			return err
		}
		if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return repoenv.Unexpected(resp.StatusCode())
		}
		items = append(items, resp.JSON200.Items...)
		more = resp.JSON200.HasMore
		if len(resp.JSON200.Items) == 0 || !resp.JSON200.HasMore {
			break
		}
	}
	if len(items) > limit {
		items, more = items[:limit], true
	}
	if jo.Enabled() {
		out := make([]commitOut, len(items))
		for i, it := range items {
			out[i] = commitOut{CommitSummary: it, URL: fmt.Sprintf("%s/commit/%s", web, it.Sha)}
		}
		if err := jo.Write(f.IO.Out, out, f.IO.OutTTY); err != nil {
			return err
		}
	} else {
		if len(items) == 0 && f.IO.OutTTY {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessun commit tocca %s\n", path)
		}
		if len(items) > 0 {
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "COMMIT", "DATA", "AUTORE", "OGGETTO")
			for _, it := range items {
				t.AddRow(short(it.Sha), it.Author.Date.UTC().Format(time.DateOnly), who(it.Author), it.Subject)
			}
			if err := t.Render(f.IO.Out); err != nil {
				return err
			}
		}
	}
	if more {
		_, _ = fmt.Fprintf(f.IO.ErrOut, "Mostrati i primi %d commit: ce ne sono altri, usa -L per vederne di più\n", len(items))
	}
	return nil
}
