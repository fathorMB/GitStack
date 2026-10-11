package notification

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// openURL apre un indirizzo nel browser; nei test si sostituisce.
var openURL = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo  output.JSONOptions
		web bool
	)
	cmd := &cobra.Command{
		Use:   "view <id>",
		Short: "Mostra una notifica e la issue collegata",
		Long: "Mostra motivo, evento, repo, issue collegata e indirizzo web di una notifica. Non la segna come letta " +
			"(usa `gs notification read`). Con --web apre la issue collegata nel browser.",
		Example: "  gs notification view 0b5c1f1e-8f0d-4c1e-9a2a-6f3f0d1b7a11\n  gs notification view <id> --web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmdutil.UsageErrorf("serve l'id della notifica: gs notification view <id>")
			}
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			if web && jo.Enabled() {
				return cmdutil.UsageErrorf("--web non si combina con --json né con --jq")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.GetNotificationWithResponse(c.Context(), id)
			if err != nil {
				return err
			}
			if err := cmdutilCheck(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			n := *resp.JSON200
			if web {
				u := e.webURL(n)
				if u == "" {
					return cmdutil.UsageErrorf("la notifica %s non ha una issue collegata da aprire", id)
				}
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
				return openURL(u)
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, e.out(n), f.IO.OutTTY)
			}
			return e.render(n)
		},
	}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Apre la issue collegata nel browser")
	output.AddJSONFlags(cmd, &jo, Fields)
	return cmd
}

func (e *env) render(n gitstack.Notification) error {
	w := e.f.IO.Out
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("%s\n", strings.TrimSpace(n.Summary))
	st := "non letta"
	switch {
	case n.Archived:
		st = "archiviata"
	case n.Read:
		st = "letta"
	}
	p("ID: %s\nStato: %s\nMotivo: %s\nEvento: %s\n", n.Id, st, n.Reason, n.Event)
	if n.Actor != nil {
		p("Da: %s\n", n.Actor.Username)
	}
	p("Data: %s\n", n.CreatedAt.UTC().Format("2006-01-02 15:04"))
	if n.Repository != nil {
		p("Repo: %s\n", n.Repository.FullName)
	}
	if n.Issue != nil {
		state := "aperta"
		if n.Issue.State == "closed" {
			state = "chiusa"
		}
		p("Issue: #%d %s (%s)\n", n.Issue.Number, n.Issue.Title, state)
	}
	if n.Webhook != nil {
		p("Webhook: %s\n", n.Webhook.Url)
	}
	if u := e.webURL(n); u != "" {
		p("URL: %s\n", u)
	}
	return nil
}
