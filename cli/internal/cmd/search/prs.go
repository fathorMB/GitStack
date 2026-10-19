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

// PullFields sono i campi di --json di `gs search prs`.
var PullFields = []string{
	"repo", "number", "title", "state", "draft", "reviewDecision", "author", "requestedReviewers",
	"sourceBranch", "targetBranch", "commentCount", "createdAt", "updatedAt", "url",
}

type pullOut struct {
	Repo string `json:"repo"`
	gitstack.PullRequestSummary
	URL string `json:"url"`
}

func newPullsCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo    output.JSONOptions
		sort  string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "prs [query]",
		Short: "Cerca Pull Request su tutta l'installazione",
		Long: "Cerca le Pull Request di tutti i repo che puoi leggere con la sintassi di ricerca I10 estesa da PR12, identica a UI e API: " +
			"is:open|closed|merged|draft, review:required|approved|changes-requested, author: e assignee: (`@me`, `@agents`; per una PR l'assegnatario è un revisore richiesto), " +
			"no:assignee, repo:owner/nome, org:, e testo libero su titolo e testo. `is:pr` è implicito; senza uno stato valgono le PR aperte. " +
			"I qualificatori ripetuti si sommano e `-` davanti li nega. Il comando non dipende dal repo corrente: per cercare in uno solo usa `repo:`.",
		Example: "  gs search prs 'review:required assignee:@me'\n" +
			"  gs search prs 'repo:acme/web is:draft author:@agents'\n" +
			"  gs search prs 'is:merged review:approved' --json repo,number,title",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			q := strings.TrimSpace(strings.Join(args, " "))
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			p := gitstack.SearchPullRequestsParams{}
			if q != "" {
				p.Q = &q
			}
			if sort != "" {
				s := gitstack.SearchPullRequestsParamsSort(strings.ToLower(sort))
				if !s.Valid() {
					return cmdutil.UsageErrorf("--sort: created, updated o comments (non %q)", sort)
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
			var items []gitstack.PullRequestSearchResult
			total := 0
			for page := 1; len(items) < limit; page++ {
				pg := page
				p.Page = &pg
				resp, err := gen.SearchPullRequestsWithResponse(c.Context(), &p)
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
				out := make([]pullOut, len(items))
				for i, it := range items {
					out[i] = pullOut{Repo: it.Repo, PullRequestSummary: it.PullRequest, URL: fmt.Sprintf("%s/%s/pulls/%d", web, it.Repo, it.PullRequest.Number)}
				}
				if err := jo.Write(f.IO.Out, out, f.IO.OutTTY); err != nil {
					return err
				}
			case len(items) == 0:
				if f.IO.OutTTY {
					_, _ = fmt.Fprintln(f.IO.ErrOut, "Nessuna Pull Request trovata")
				}
			default:
				t := output.NewTable(f.IO.OutTTY, f.IO.Width, "REPO", "#", "STATO", "REVIEW", "AGGIORNATA", "TITOLO")
				for _, it := range items {
					st := string(it.PullRequest.State)
					if it.PullRequest.Draft {
						st = "draft"
					}
					t.AddRow(it.Repo, fmt.Sprintf("#%d", it.PullRequest.Number), st, string(it.PullRequest.ReviewDecision),
						it.PullRequest.UpdatedAt.UTC().Format(time.DateOnly), it.PullRequest.Title)
				}
				if err := t.Render(f.IO.Out); err != nil {
					return err
				}
			}
			if total > len(items) {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Mostrate %d Pull Request su %d: usa -L per vederne di più o restringi la query\n", len(items), total)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&sort, "sort", "", "Ordine decrescente: created, updated o comments")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di Pull Request")
	output.AddJSONFlags(cmd, &jo, PullFields)
	return cmd
}
