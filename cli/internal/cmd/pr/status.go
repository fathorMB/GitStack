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

func newStatusCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Mostra le Pull Request aperte che ti riguardano",
		Long: "Mostra due cose: la PR aperta del ramo corrente (se c'è, con `--head` si sceglie un altro ramo) e le PR aperte " +
			"create da te nel repo. Non modifica nulla. Fuori da un ramo, la prima parte dice che non c'è un ramo.",
		Example: "  gs pr status\n  gs pr status --json current,authored",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			branch := ""
			if f.GitOutput != nil {
				if b, err := f.GitOutput(c.Context(), "branch", "--show-current"); err == nil {
					branch = strings.TrimSpace(b)
				}
			}
			out := statusOut{Authored: []summaryOut{}}
			if branch != "" {
				items, err := listPRs(c, e, gitstack.ListPullRequestsParams{Head: &branch, State: openState()})
				if err != nil {
					return err
				}
				if len(items) > 0 {
					out.Current = &summaryOut{PullRequestSummary: items[0], URL: webURL(e, items[0].Number)}
				}
			}
			q := "is:open author:@me"
			mine, err := listPRs(c, e, gitstack.ListPullRequestsParams{Q: &q})
			if err != nil {
				return err
			}
			for _, s := range mine {
				out.Authored = append(out.Authored, summaryOut{PullRequestSummary: s, URL: webURL(e, s.Number)})
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			w := f.IO.Out
			switch {
			case branch == "":
				_, _ = fmt.Fprintln(w, "Ramo corrente: nessuno (HEAD staccato o fuori da un repo)")
			case out.Current == nil:
				_, _ = fmt.Fprintf(w, "Ramo corrente %s: nessuna PR aperta\n", branch)
			default:
				p := out.Current.PullRequestSummary
				_, _ = fmt.Fprintf(w, "Ramo corrente %s: PR #%d %s (%s), review %s\n", branch, p.Number, p.Title, stateLabel(p.State, p.Draft), p.ReviewDecision)
			}
			_, _ = fmt.Fprintln(w, "")
			if len(out.Authored) == 0 {
				_, err := fmt.Fprintln(w, "Nessuna PR aperta create da te")
				return err
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "#", "STATO", "BRANCH", "REVIEW", "TITOLO")
			for _, s := range out.Authored {
				t.AddRow(fmt.Sprintf("#%d", s.Number), stateLabel(s.State, s.Draft),
					s.SourceBranch+" -> "+s.TargetBranch, string(s.ReviewDecision), s.Title)
			}
			return t.Render(w)
		},
	}
	output.AddJSONFlags(cmd, &jo, StatusFields)
	return cmd
}

// openState è lo stato `open` per il filtro di ListPullRequests.
func openState() *gitstack.ListPullRequestsParamsState {
	s := gitstack.ListPullRequestsParamsStateOpen
	return &s
}

// listPRs legge la prima pagina (fino a 30) di un elenco di PR.
func listPRs(c *cobra.Command, e *repoenv.Env, p gitstack.ListPullRequestsParams) ([]gitstack.PullRequestSummary, error) {
	per := 30
	page := 1
	p.PerPage = &per
	p.Page = &page
	resp, err := e.Gen.ListPullRequestsWithResponse(c.Context(), e.Owner, e.Repo, &p)
	if err != nil {
		return nil, err
	}
	if err := check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	return resp.JSON200.Items, nil
}
