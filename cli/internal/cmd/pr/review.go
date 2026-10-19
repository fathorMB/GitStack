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

func newReviewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo             output.JSONOptions
		approve        bool
		requestChanges bool
		comment        bool
		body           string
		file           string
		commit         string
	)
	cmd := &cobra.Command{
		Use:   "review <numero>",
		Short: "Invia una review alla Pull Request",
		Long: "Invia una review con uno di tre esiti: `--comment` (osservazioni), `--approve` o `--request-changes`. " +
			"Serve esattamente un esito. `--request-changes` vuole un testo (`--body` o `--body-file`). Approvare e chiedere modifiche " +
			"richiede `write` e non vale sulla propria PR (403). Un agente può approvare, ma la sua approvazione non conta (PR2). " +
			"I commenti sulle singole righe non hanno flag: si inviano con `gs api` (vedi la skill gitstack-pr-review).",
		Example: "  gs pr review 7 --comment -b \"Nota su cli/x.go\"\n" +
			"  gs pr review 7 --approve\n  gs pr review 7 --request-changes -F review.txt --json state,countsAsApproval",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			var ev gitstack.ReviewEvent
			picked := 0
			if approve {
				ev, picked = gitstack.Approve, picked+1
			}
			if requestChanges {
				ev, picked = gitstack.RequestChanges, picked+1
			}
			if comment {
				ev, picked = gitstack.Comment, picked+1
			}
			if picked != 1 {
				return cmdutil.UsageErrorf("scegli un esito: --comment, --approve o --request-changes (uno solo)")
			}
			text, set, err := readText(f, body, c.Flags().Changed("body"), file)
			if err != nil {
				return err
			}
			if ev == gitstack.RequestChanges && strings.TrimSpace(text) == "" {
				return cmdutil.UsageErrorf("--request-changes vuole un testo che spieghi cosa cambiare: --body o --body-file")
			}
			in := gitstack.CreatePullRequestReviewInput{Event: ev}
			if set && strings.TrimSpace(text) != "" {
				in.Body = &text
			}
			if sha := strings.TrimSpace(commit); sha != "" {
				in.CommitSha = &sha
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			resp, err := e.Gen.CreatePullRequestReviewWithResponse(c.Context(), e.Owner, e.Repo, n, in)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			r := resp.JSON201
			if jo.Enabled() {
				return jo.Write(f.IO.Out, r, f.IO.OutTTY)
			}
			conta := "no"
			if r.CountsAsApproval {
				conta = "sì"
			}
			_, err = fmt.Fprintf(f.IO.Out, "review %s inviata alla PR #%d (conta come approvazione: %s)\n", r.State, n, conta)
			return err
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&comment, "comment", false, "Esito: solo commenti")
	fl.BoolVar(&approve, "approve", false, "Esito: approva")
	fl.BoolVar(&requestChanges, "request-changes", false, "Esito: chiede modifiche (vuole un testo)")
	fl.StringVarP(&body, "body", "b", "", "Testo generale della review")
	fl.StringVarP(&file, "body-file", "F", "", "Testo letto da un file (`-` = stdin)")
	fl.StringVar(&commit, "commit", "", "Punta della PR che hai visto (SHA): se è cambiata la review è rifiutata (409 head_changed)")
	output.AddJSONFlags(cmd, &jo, ReviewFields)
	return cmd
}
