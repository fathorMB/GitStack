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

func newCommentCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo   output.JSONOptions
		body string
		file string
	)
	cmd := &cobra.Command{
		Use:   "comment <numero>",
		Short: "Commenta una Pull Request",
		Long: "Aggiunge un commento generale alla PR (non sulle righe: per quelli c'è `gs pr review` o `gs api`). " +
			"Serve il permesso di lettura sul repo. Su stdout l'indirizzo della PR.",
		Example: "  gs pr comment 7 --body \"Pronta per la review: @alice\"\n  gs pr comment 7 -F comment.txt --json id,createdAt",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			text, set, err := readText(f, body, c.Flags().Changed("body"), file)
			if err != nil {
				return err
			}
			if !set || strings.TrimSpace(text) == "" {
				return cmdutil.UsageErrorf("serve il testo del commento: --body o --body-file")
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			resp, err := e.Gen.CreatePullRequestCommentWithResponse(c.Context(), e.Owner, e.Repo, n, gitstack.CreatePullRequestCommentInput{Body: text})
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, commentOut{PullRequestComment: *resp.JSON201, URL: webURL(e, n)}, f.IO.OutTTY)
			}
			_, err = fmt.Fprintln(f.IO.Out, webURL(e, n))
			return err
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&body, "body", "b", "", "Testo del commento in Markdown")
	fl.StringVarP(&file, "body-file", "F", "", "Testo letto da un file (`-` = stdin)")
	output.AddJSONFlags(cmd, &jo, CommentFields)
	return cmd
}
