// Package api è il gruppo `gs api`. In questo item c'è solo il sottocomando
// provvisorio `gs api user`, che esercita configurazione, client, output e
// codici di uscita; GIT-169 lo sostituisce con `gs api <endpoint>`.
package api

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// SessionFields sono i campi di --json per `gs api user` (CurrentSession).
var SessionFields = []string{"authMethod", "expiresAt", "mustChangePassword", "scopes", "user"}

// NewCmd restituisce il comando padre `gs api`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Chiama l'API di GitStack",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(newUserCmd(f))
	return cmd
}

func newUserCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Mostra chi sei per l'istanza corrente (GET /auth/session)",
		Args:  cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cl, err := api.FromFactory(f)
			if err != nil {
				return err
			}
			resp, err := cl.Generated().GetCurrentSessionWithResponse(c.Context())
			if err != nil {
				return err
			}
			if err := api.CheckStatus(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, resp.JSON200, f.IO.OutTTY)
			}
			s := resp.JSON200
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "CAMPO", "VALORE")
			t.AddRow("user", string(s.User.Username))
			t.AddRow("nome", s.User.DisplayName)
			t.AddRow("autenticazione", string(s.AuthMethod))
			return t.Render(f.IO.Out)
		},
	}
	output.AddJSONFlags(cmd, &jo, SessionFields)
	return cmd
}
