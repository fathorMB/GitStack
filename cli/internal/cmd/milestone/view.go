package milestone

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "view <numero>",
		Short:   "Mostra una milestone",
		Long: "Mostra i dettagli di una milestone: numero, titolo, descrizione, stato, " +
			"data di scadenza e avanzamento (issue chiuse / totali).",
		Example: "  gs milestone view 3\n  gs milestone view #3",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.GetMilestoneWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.MilestoneNumberParam(n))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			m := resp.JSON200
			if !jo.Enabled() {
				pct := milestoneProgress(*m)
				fmt.Fprintf(e.f.IO.Out, "%s/%s\n", e.owner, e.repo)
				fmt.Fprintf(e.f.IO.Out, "  Titolo:    %s\n", m.Title)
				fmt.Fprintf(e.f.IO.Out, "  Numero:    #%d\n", m.Number)
				fmt.Fprintf(e.f.IO.Out, "  Stato:     %s\n", m.State)
				if m.Description != "" {
					fmt.Fprintf(e.f.IO.Out, "  Descrizione: %s\n", m.Description)
				}
				if m.DueOn != nil {
					fmt.Fprintf(e.f.IO.Out, "  Scadenza:  %s\n", m.DueOn.Format("2006-01-02"))
				}
				fmt.Fprintf(e.f.IO.Out, "  Avanzamento: %s (%d/%d chiuse)\n", pct, m.ClosedIssues, m.OpenIssues+m.ClosedIssues)
				fmt.Fprintf(e.f.IO.Out, "  Pagina web: %s\n", e.webURL(m.Number))
				return nil
			}
			out := milestoneOut{Milestone: *m, URL: e.webURL(m.Number), Progress: milestoneProgress(*m)}
			return jo.Write(e.f.IO.Out, out, e.f.IO.OutTTY)
		},
	}
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}
