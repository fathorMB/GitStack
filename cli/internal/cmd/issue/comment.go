package issue

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newCommentCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo       output.JSONOptions
		body     string
		bodyFile string
	)
	cmd := &cobra.Command{
		Use:   "comment <numero>",
		Short: "Commenta una issue",
		Long: "Aggiunge un commento Markdown. Il testo viene da --body o da --body-file (`-` legge stdin). Chi vede il repo " +
			"commenta anche le issue chiuse (I3); con la discussione bloccata (I11) solo chi ha write.",
		Example: "  gs issue comment 12 --body \"Riproduco, grazie\"\n  gs issue comment 12 -F risposta.md",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			text, set, err := readBody(f, body, c.Flags().Changed("body"), bodyFile)
			if err != nil {
				return err
			}
			if !set || text == "" {
				return cmdutil.UsageErrorf("serve il testo del commento: usa --body o --body-file")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.CreateIssueCommentWithResponse(c.Context(), e.owner, e.repo, n, gitstack.CreateIssueCommentInput{Body: text})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			url := fmt.Sprintf("%s#comment-%s", e.webURL(n), resp.JSON201.Id)
			if jo.Enabled() {
				return jo.Write(f.IO.Out, commentOut{IssueComment: *resp.JSON201, URL: url}, f.IO.OutTTY)
			}
			_, err = fmt.Fprintln(f.IO.Out, url)
			return err
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&body, "body", "b", "", "Testo del commento (Markdown)")
	fl.StringVarP(&bodyFile, "body-file", "F", "", "Legge il testo da un file (`-` per stdin)")
	output.AddJSONFlags(cmd, &jo, CommentFields)
	return cmd
}
