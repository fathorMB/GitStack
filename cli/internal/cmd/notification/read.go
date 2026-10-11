package notification

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func cmdutilCheck(status int, body []byte) error { return api.CheckStatus(status, body) }

// readResult è l'output JSON di `read`.
type readResult struct {
	Marked int `json:"marked"`
}

func newReadCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo      output.JSONOptions
		all     bool
		reasons []string
	)
	cmd := &cobra.Command{
		Use:   "read [<id>]",
		Short: "Segna come lette una notifica o tutte",
		Long: "Segna come letta la notifica <id> (idempotente), oppure con --all tutte le non lette; --all si restringe con " +
			"--reason e -R/--repo. Le archiviate non cambiano.",
		Example: "  gs notification read 0b5c1f1e-8f0d-4c1e-9a2a-6f3f0d1b7a11\n  gs notification read --all -R alice/web --reason assigned",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 0:
				return cmdutil.UsageErrorf("--all non si combina con un id")
			case !all && len(args) != 1:
				return cmdutil.UsageErrorf("serve l'id della notifica o --all: gs notification read <id>|--all")
			case !all && len(reasons) > 0:
				return cmdutil.UsageErrorf("--reason vale solo con --all")
			case !all && f.RepoFlag != "":
				return cmdutil.UsageErrorf("-R/--repo vale solo con --all")
			}
			var (
				id gitstack.NotificationIdParam
				p  gitstack.MarkAllNotificationsReadParams
			)
			var err error
			if all {
				if p.Reason, err = reasonFilter(reasons); err != nil {
					return err
				}
				if p.Repo, err = repoFilter(f); err != nil {
					return err
				}
			} else if id, err = parseID(args[0]); err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if all {
				resp, err := e.gen.MarkAllNotificationsReadWithResponse(c.Context(), &p)
				if err != nil {
					return err
				}
				if err := cmdutilCheck(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				if jo.Enabled() {
					return jo.Write(f.IO.Out, readResult{Marked: resp.JSON200.Marked}, f.IO.OutTTY)
				}
				_, _ = fmt.Fprintf(f.IO.Out, "%d notifiche segnate come lette\n", resp.JSON200.Marked)
				return nil
			}
			yes := true
			resp, err := e.gen.UpdateNotificationWithResponse(c.Context(), id, gitstack.UpdateNotificationInput{Read: &yes})
			if err != nil {
				return err
			}
			if err := cmdutilCheck(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, e.out(*resp.JSON200), f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "Notifica %s segnata come letta\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Segna come lette tutte le non lette")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Con --all: solo questi motivi")
	// Con --all i campi sono {marked}: nessun elenco di campi da validare.
	output.AddJSONFlags(cmd, &jo, append([]string{"marked"}, Fields...))
	return cmd
}
