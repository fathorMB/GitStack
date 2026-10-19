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

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo        output.JSONOptions
		title     string
		body      string
		file      string
		head      string
		base      string
		draft     bool
		reviewers []string
		web       bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Apri una Pull Request",
		Long: "Apre una Pull Request dal ramo di origine (`--head`, predefinito il ramo corrente) verso quello di destinazione " +
			"(`--base`, predefinito il branch principale del repo). Serve `write`. Con `--draft` la PR è in bozza: non si unisce " +
			"e non chiede review finché `gs pr ready` non la segna pronta (PR6). I revisori di una bozza sono ignorati (PR12): " +
			"si indicano con `gs pr ready --reviewer`. Il testo con `fixes #n` chiude la issue al merge (PR8).",
		Example: "  gs pr create --draft --title \"Correggi il crash\" --body \"fixes #12\"\n" +
			"  gs pr create -t \"Correggi il crash\" -F msg.txt --reviewer alice\n" +
			"  gs pr create --head agent/12-crash --base main --json number,url",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cmdutil.UsageErrorf("argomento inatteso %q per %q", args[0], c.CommandPath())
			}
			if web && jo.Enabled() {
				return cmdutil.UsageErrorf("--web non si combina con --json né con --jq")
			}
			text, set, err := readText(f, body, c.Flags().Changed("body"), file)
			if err != nil {
				return err
			}
			if strings.TrimSpace(title) == "" {
				return cmdutil.UsageErrorf("serve un titolo: --title \"...\"")
			}
			src := strings.TrimSpace(head)
			if src == "" {
				if src, err = currentBranch(c, f); err != nil {
					return err
				}
			}
			in := gitstack.CreatePullRequestInput{Title: title, SourceBranch: src}
			if set {
				in.Body = &text
			}
			if b := strings.TrimSpace(base); b != "" {
				in.TargetBranch = &b
			}
			if draft {
				d := true
				in.Draft = &d
			}
			if rs := cleanNames(reviewers); len(rs) > 0 {
				in.Reviewers = &rs
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			resp, err := e.Gen.CreatePullRequestWithResponse(c.Context(), e.Owner, e.Repo, in)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			if web {
				u := webURL(e, resp.JSON201.Number)
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
				return f.OpenBrowser(u)
			}
			if !jo.Enabled() && f.IO.OutTTY {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "PR #%d aperta (%s): %s -> %s\n", resp.JSON201.Number, stateLabel(resp.JSON201.State, resp.JSON201.Draft), src, resp.JSON201.TargetBranch)
			}
			return printPR(f, &jo, e, resp.JSON201)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&title, "title", "t", "", "Titolo (obbligatorio)")
	fl.StringVarP(&body, "body", "b", "", "Testo in Markdown; le parole chiave `fixes #n` collegano le issue")
	fl.StringVarP(&file, "body-file", "F", "", "Testo letto da un file (`-` = stdin)")
	fl.StringVarP(&head, "head", "H", "", "Ramo di origine (predefinito: il ramo corrente)")
	fl.StringVarP(&base, "base", "B", "", "Ramo di destinazione (predefinito: il branch principale)")
	fl.BoolVarP(&draft, "draft", "d", false, "Apri in bozza")
	fl.StringSliceVar(&reviewers, "reviewer", nil, "Revisore richiesto, nome utente (ripetibile; ignorato per una bozza)")
	fl.BoolVarP(&web, "web", "w", false, "Apre la PR nel browser dopo la creazione")
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}

// cleanNames toglie `@`, spazi e doppioni dai nomi utente dati a mano.
func cleanNames(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range in {
		n = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(n), "@"))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
