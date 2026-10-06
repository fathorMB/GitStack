package milestone

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
		jo    output.JSONOptions
		state string
		limit int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le milestone del repo",
		Long: "Elenca le milestone del repo, dalla più recente. Richiede il permesso read. " +
			"Con --state closed mostra solo le milestone chiuse, con all tutte le milestone.",
		Example: "  gs milestone list\n  gs milestone list --state all\n  gs milestone list --json title,state",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			var stateFilter *gitstack.ListMilestonesParamsState
			if c.Flags().Changed("state") {
				switch strings.ToLower(state) {
				case "open", "closed", "all":
					s := gitstack.ListMilestonesParamsState(strings.ToLower(state))
					stateFilter = &s
				default:
					return cmdutil.UsageErrorf("--state: open, closed o all (non %q)", state)
				}
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			items, err := e.listMilestones(c.Context(), limit, stateFilter)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuna milestone in %s/%s\n", e.owner, e.repo)
				}
				return nil
			}
			if jo.Enabled() {
				out := make([]milestoneOut, len(items))
				for i, m := range items {
					out[i] = milestoneOut{Milestone: m, URL: e.webURL(m.Number), Progress: milestoneProgress(m)}
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "NUMERO", "STATO", "AVANZAMENTO", "DATA SCADENZA", "TITOLO")
			for _, m := range items {
				var due string
				if m.DueOn != nil {
					due = m.DueOn.Format("2006-01-02")
				}
				pct := milestoneProgress(m)
				t.AddRow(fmt.Sprintf("#%d", m.Number), string(m.State), pct, due, m.Title)
			}
			return t.Render(f.IO.Out)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&state, "state", "s", "open", "Stato: open, closed o all")
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di milestone")
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}
