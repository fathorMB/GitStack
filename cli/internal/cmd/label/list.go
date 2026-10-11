package label

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo    output.JSONOptions
		limit int
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le etichette del repo",
		Long:    "Elenca le etichette del repo, dalla più recente. Richiede il permesso read.",
		Example: "  gs label list\n  gs label list --limit 5\n  gs label list --json name,color",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			items, err := e.listLabels(c.Context(), limit)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuna etichetta in %s/%s\n", e.owner, e.repo)
				}
				return nil
			}
			if jo.Enabled() {
				out := make([]labelOut, len(items))
				for i, l := range items {
					out[i] = labelOut{Label: l, URL: e.webURL(l.Name)}
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "NOME", "COLORE", "ISSUE APERTE")
			for _, l := range items {
				t.AddRow(l.Name, l.Color, fmt.Sprintf("%d", l.OpenIssues))
			}
			return t.Render(f.IO.Out)
		},
	}
	fl := cmd.Flags()
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di etichette")
	output.AddJSONFlags(cmd, &jo, LabelFields)
	return cmd
}
