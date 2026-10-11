package issue

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo        output.JSONOptions
		state     string
		labels    []string
		assignee  string
		author    string
		milestone string
		search    string
		limit     int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le issue del repo",
		Long: "Elenca le issue del repo, dalla più recente. I filtri si sommano a --search, che usa la sintassi di ricerca " +
			"delle issue (I10) identica a UI e API: is:open|closed, reason:, label:, assignee:, author:, milestone:, no:, e testo libero.\n\n" +
			"Senza --state e senza `is:` in --search l'elenco è delle issue aperte.",
		Example: "  gs issue list --assignee @me --label bug\n" +
			"  gs issue list --state all --author alice --milestone v1\n" +
			"  gs issue list --search 'is:closed reason:duplicate crash' --json number,title",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			p := gitstack.ListIssuesParams{}
			if c.Flags().Changed("state") {
				switch strings.ToLower(state) {
				case "open", "closed", "all":
					s := gitstack.ListIssuesParamsState(strings.ToLower(state))
					p.State = &s
				default:
					return cmdutil.UsageErrorf("--state: open, closed o all (non %q)", state)
				}
			}
			if l := dedupe(labels); len(l) > 0 {
				s := strings.Join(l, ",")
				p.Labels = &s
			}
			if assignee != "" {
				p.Assignee = &assignee
			}
			if author != "" {
				p.Author = &author
			}
			if milestone != "" {
				p.Milestone = &milestone
			}
			if search != "" {
				p.Q = &search
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			per := limit
			if per > 100 {
				per = 100
			}
			p.PerPage = &per
			var items []gitstack.IssueSummary
			for page := 1; len(items) < limit; page++ {
				pg := page
				p.Page = &pg
				resp, err := e.gen.ListIssuesWithResponse(c.Context(), e.owner, e.repo, &p)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				items = append(items, resp.JSON200.Items...)
				if len(resp.JSON200.Items) == 0 || page*per >= resp.JSON200.Total {
					break
				}
			}
			if len(items) > limit {
				items = items[:limit]
			}
			if jo.Enabled() {
				out := make([]summaryOut, len(items))
				for i, s := range items {
					out[i] = summaryOut{IssueSummary: s, URL: e.webURL(s.Number)}
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuna issue in %s/%s con questi filtri\n", e.owner, e.repo)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "#", "STATO", "ETICHETTE", "AGGIORNATA", "TITOLO")
			for _, s := range items {
				var ls []string
				for _, l := range s.Labels {
					ls = append(ls, l.Name)
				}
				st := string(s.State)
				if s.CloseReason != nil {
					st += ":" + string(*s.CloseReason)
				}
				t.AddRow(fmt.Sprintf("#%d", s.Number), st, strings.Join(ls, ","), day(s.UpdatedAt), s.Title)
			}
			return t.Render(f.IO.Out)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&state, "state", "s", "open", "Stato: open, closed o all")
	fl.StringSliceVarP(&labels, "label", "l", nil, "Solo con queste etichette (tutte; ripetibile)")
	fl.StringVarP(&assignee, "assignee", "a", "", "Assegnatario: utente, `@me`, `@agents` o `none`")
	fl.StringVarP(&author, "author", "A", "", "Autore: utente o `@me`")
	fl.StringVarP(&milestone, "milestone", "m", "", "Milestone, per numero o titolo (`none` per nessuna)")
	fl.StringVarP(&search, "search", "S", "", "Ricerca con la sintassi I10 (is:, reason:, label:, ..., testo libero)")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di issue")
	output.AddJSONFlags(cmd, &jo, SummaryFields)
	return cmd
}
