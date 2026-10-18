// Package mcp è `gs mcp`: il server MCP (Model Context Protocol) su stdio per
// gli assistenti di coding (v1.1, regole M1-M5). Usa il login, il token e le
// istanze di gs; gli strumenti sono generati dal contratto OpenAPI.
package mcp

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	servermcp "github.com/fathorMB/GitStack/cli/internal/mcp"
)

// NewCmd restituisce `gs mcp`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	var readOnly bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Avvia il server MCP di GitStack su stdio",
		Long: "Avvia un server MCP (Model Context Protocol) su stdin/stdout per gli assistenti di coding. Usa il login, " +
			"il token e l'istanza di gs (`gs auth login`, GS_TOKEN, GS_HOST); --host sceglie l'istanza.\n\n" +
			"Gli strumenti dedicati (oggetto_azione: pr_list, issue_create, code_file...) sono generati dalle operazioni " +
			"del contratto OpenAPI segnate x-mcp.expose, e api_request chiama qualsiasi altra operazione entro i permessi del " +
			"token. Le azioni distruttive (chiusure, merge, eliminazioni, occultamenti) portano l'annotazione destructive; " +
			"il server non chiede conferme: le chiede l'assistente. Non c'è uno strumento dedicato per eliminare o archiviare " +
			"repository, né per impostazioni, webhook, token e amministrazione.\n\n" +
			"Offre anche i prompt issue_to_pr, issue_triage e pr_review (M4), generati dalle skills di skills/; nessuna risorsa MCP.\n\n" +
			"--read-only limita il server alle sole letture, api_request compreso.\n\n" +
			"Se gs è più vecchio dell'istanza nella stessa versione maggiore, il server parte e avvisa su stderr di " +
			"aggiornare; con una versione maggiore diversa non parte (M5). Stdout è riservato al protocollo.",
		Example: "  gs mcp\n  gs mcp --read-only\n  gs mcp --host git.example.com",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cl, err := api.FromFactory(f)
			if err != nil {
				return err
			}
			srv := servermcp.NewServer(servermcp.Options{
				Client:   cl,
				Version:  f.Version,
				ReadOnly: readOnly,
				In:       f.IO.In,
				Out:      f.IO.Out,
			})
			return srv.Serve(c.Context())
		},
	}
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "Solo letture: rifiuta le scritture, anche via api_request")
	cmd.Flags().StringVar(&f.HostnameFlag, "host", "", "Istanza di GitStack (host[:porta]); come --hostname")
	return cmd
}
