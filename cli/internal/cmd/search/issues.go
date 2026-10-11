package search

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

// IssueFields sono i campi di --json di `gs search issues`.
var IssueFields = []string{
	"repo", "number", "title", "state", "closeReason", "author", "labels", "assignees",
	"milestone", "locked", "commentCount", "createdAt", "updatedAt", "url",
}

type issueOut struct {
	Repo string `json:"repo"`
	gitstack.IssueSummary
	URL string `json:"url"`
}

func newIssuesCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo    output.JSONOptions
		sort  string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "issues <query>",
		Short: "Cerca issue su tutta l'installazione",
		Long: "Cerca le issue di tutti i repo che puoi leggere con la sintassi di ricerca I10, identica a UI e API: " +
			"is:open|closed, reason:, label:, assignee: (`@me`, `@agents`), author:, milestone:, no:, repo:owner/nome, org:, e testo libero. " +
			"I qualificatori ripetuti si sommano e `-` davanti li nega. Il comando non dipende dal repo corrente: per cercare in uno solo usa `repo:` o `gs issue list`.",
		Example: "  gs search issues 'is:open label:bug assignee:@me'\n" +
			"  gs search issues 'repo:acme/web crash' --sort updated\n" +
			"  gs search issues 'is:closed reason:duplicate' --json repo,number,title",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			q := strings.TrimSpace(strings.Join(args, " "))
			if q == "" {
				return cmdutil.UsageErrorf("serve una query: gs search issues <query>")
			}
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			p := gitstack.SearchIssuesParams{Q: &q}
			if sort != "" {
				s := gitstack.SearchIssuesParamsSort(strings.ToLower(sort))
				if !s.Valid() {
					return cmdutil.UsageErrorf("--sort: created, updated, comments o relevance (non %q)", sort)
				}
				p.Sort = &s
			}
			gen, host, err := repoenv.Client(f)
			if err != nil {
				return err
			}
			per := limit
			if per > 100 {
				per = 100
			}
			p.PerPage = &per
			var items []gitstack.IssueSearchResult
			total := 0
			for page := 1; len(items) < limit; page++ {
				pg := page
				p.Page = &pg
				resp, err := gen.SearchIssuesWithResponse(c.Context(), &p)
				if err != nil {
					return err
				}
				if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return repoenv.Unexpected(resp.StatusCode())
				}
				total = resp.JSON200.Total
				items = append(items, resp.JSON200.Items...)
				if len(resp.JSON200.Items) == 0 || page*per >= total {
					break
				}
			}
			if len(items) > limit {
				items = items[:limit]
			}
			web := repoenv.WebBase(host)
			switch {
			case jo.Enabled():
				out := make([]issueOut, len(items))
				for i, it := range items {
					out[i] = issueOut{Repo: it.Repo, IssueSummary: it.Issue, URL: fmt.Sprintf("%s/%s/issues/%d", web, it.Repo, it.Issue.Number)}
				}
				if err := jo.Write(f.IO.Out, out, f.IO.OutTTY); err != nil {
					return err
				}
			case len(items) == 0:
				if f.IO.OutTTY {
					_, _ = fmt.Fprintln(f.IO.ErrOut, "Nessuna issue trovata")
				}
			default:
				t := output.NewTable(f.IO.OutTTY, f.IO.Width, "REPO", "#", "STATO", "AGGIORNATA", "TITOLO")
				for _, it := range items {
					st := string(it.Issue.State)
					if it.Issue.CloseReason != nil {
						st += ":" + string(*it.Issue.CloseReason)
					}
					t.AddRow(it.Repo, fmt.Sprintf("#%d", it.Issue.Number), st, it.Issue.UpdatedAt.UTC().Format(time.DateOnly), it.Issue.Title)
				}
				if err := t.Render(f.IO.Out); err != nil {
					return err
				}
			}
			if total > len(items) {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Mostrate %d issue su %d: usa -L per vederne di più o restringi la query\n", len(items), total)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&sort, "sort", "", "Ordine decrescente: created, updated, comments o relevance (solo con testo libero)")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di issue")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}
