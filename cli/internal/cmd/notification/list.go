package notification

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
		jo       output.JSONOptions
		all      bool
		archived bool
		reasons  []string
		limit    int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le notifiche",
		Long: "Elenca le notifiche della casella, dalla più recente. Di default solo le non lette; con --all anche le " +
			"lette, con --archived le archiviate. I filtri: --reason (uno o più motivi) e -R/--repo.",
		Example: "  gs notification list\n  gs notification list --all --reason assigned,mentioned\n" +
			"  gs notification list -R alice/web --json id,reason,summary,url",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			if all && archived {
				return cmdutil.UsageErrorf("--all e --archived sono alternativi")
			}
			p := gitstack.ListNotificationsParams{}
			var err error
			if p.Reason, err = reasonFilter(reasons); err != nil {
				return err
			}
			if p.Repo, err = repoFilter(f); err != nil {
				return err
			}
			switch {
			case all:
				s := gitstack.ListNotificationsParamsStateAll
				p.State = &s
			case archived:
				s := gitstack.ListNotificationsParamsStateArchived
				p.State = &s
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
			var items []gitstack.Notification
			for page := 1; len(items) < limit; page++ {
				pg := page
				p.Page = &pg
				resp, err := e.gen.ListNotificationsWithResponse(c.Context(), &p)
				if err != nil {
					return err
				}
				if err := cmdutilCheck(resp.StatusCode(), resp.Body); err != nil {
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
				out := make([]notificationOut, len(items))
				for i, n := range items {
					out[i] = e.out(n)
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintln(f.IO.ErrOut, "Nessuna notifica con questi filtri")
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "ID", "STATO", "MOTIVO", "REPO", "ISSUE", "DATA", "RIEPILOGO")
			for _, n := range items {
				repo, issue := "", ""
				if n.Repository != nil {
					repo = n.Repository.FullName
				}
				if n.Issue != nil {
					issue = fmt.Sprintf("#%d", n.Issue.Number)
				}
				st := "non letta"
				switch {
				case n.Archived:
					st = "archiviata"
				case n.Read:
					st = "letta"
				}
				t.AddRow(n.Id.String(), st, string(n.Reason), repo, issue, n.CreatedAt.UTC().Format("2006-01-02"), strings.TrimSpace(n.Summary))
			}
			return t.Render(f.IO.Out)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&all, "all", "a", false, "Anche le notifiche già lette")
	fl.BoolVar(&archived, "archived", false, "Solo le archiviate")
	fl.StringSliceVar(&reasons, "reason", nil, "Solo questi motivi ("+strings.Join(Reasons, ", ")+"; ripetibile o separati da virgola)")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di notifiche")
	output.AddJSONFlags(cmd, &jo, Fields)
	return cmd
}
