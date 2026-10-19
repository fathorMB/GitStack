package pr

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo     output.JSONOptions
		state  string
		search string
		head   string
		base   string
		sort   string
		limit  int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le Pull Request del repo",
		Long: "Elenca le Pull Request del repo, dalla più recente. `--search` usa la sintassi I10 estesa alle PR (PR12): " +
			"is:open|closed|merged, is:draft, review:required|approved|changes-requested, author:utente|@me|@agents, label:, no:, e testo libero. " +
			"Senza --state vale `open`; `--state` si manda solo se lo scrivi.",
		Example: "  gs pr list\n  gs pr list --state all --search 'review:required author:@agents'\n" +
			"  gs pr list --head agent/12-crash --json number,title,state",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			p := gitstack.ListPullRequestsParams{}
			if c.Flags().Changed("state") {
				switch strings.ToLower(state) {
				case "open", "closed", "merged", "all":
					s := gitstack.ListPullRequestsParamsState(strings.ToLower(state))
					p.State = &s
				default:
					return cmdutil.UsageErrorf("--state: open, closed, merged o all (non %q)", state)
				}
			}
			if sort != "" {
				s := gitstack.ListPullRequestsParamsSort(strings.ToLower(sort))
				switch s {
				case gitstack.ListPullRequestsParamsSortCreated, gitstack.ListPullRequestsParamsSortUpdated, gitstack.ListPullRequestsParamsSortComments:
				default:
					return cmdutil.UsageErrorf("--sort: created, updated o comments (non %q)", sort)
				}
				p.Sort = &s
			}
			if search != "" {
				p.Q = &search
			}
			if head != "" {
				p.Head = &head
			}
			if base != "" {
				p.Base = &base
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			per := limit
			if per > 100 {
				per = 100
			}
			p.PerPage = &per
			var items []gitstack.PullRequestSummary
			total := 0
			for page := 1; len(items) < limit; page++ {
				pg := page
				p.Page = &pg
				resp, err := e.Gen.ListPullRequestsWithResponse(c.Context(), e.Owner, e.Repo, &p)
				if err != nil {
					return err
				}
				if err := check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
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
			if jo.Enabled() {
				out := make([]summaryOut, len(items))
				for i, s := range items {
					out[i] = summaryOut{PullRequestSummary: s, URL: webURL(e, s.Number)}
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuna Pull Request in %s/%s con questi filtri\n", e.Owner, e.Repo)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "#", "STATO", "BRANCH", "REVIEW", "AGGIORNATA", "TITOLO")
			for _, s := range items {
				t.AddRow(fmt.Sprintf("#%d", s.Number), stateLabel(s.State, s.Draft),
					s.SourceBranch+" -> "+s.TargetBranch, string(s.ReviewDecision), day(s.UpdatedAt), s.Title)
			}
			if err := t.Render(f.IO.Out); err != nil {
				return err
			}
			if total > len(items) {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Mostrate %d Pull Request su %d: usa -L per vederne di più o restringi la ricerca\n", len(items), total)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&state, "state", "s", "open", "Stato: open, closed, merged o all")
	fl.StringVarP(&search, "search", "S", "", "Ricerca con la sintassi I10 estesa alle PR (is:, review:, author:, label:, testo libero)")
	fl.StringVarP(&head, "head", "H", "", "Solo le PR con questo ramo di origine")
	fl.StringVarP(&base, "base", "B", "", "Solo le PR verso questo ramo di destinazione")
	fl.StringVar(&sort, "sort", "", "Ordine decrescente: created, updated o comments")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di Pull Request")
	output.AddJSONFlags(cmd, &jo, SummaryFields)
	return cmd
}
