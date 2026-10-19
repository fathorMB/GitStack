package pr

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
)

func newCheckoutCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "checkout <numero>",
		Short: "Passa al ramo di origine di una Pull Request",
		Long: "Scarica dal remote `origin` il ramo di origine della PR (`git fetch`) e vi passa (`git switch`). " +
			"Se il ramo locale esiste già e ha commit diversi, `gs` non fa merge né reset: lo dice git. " +
			"Funziona solo nel repo il cui remote `origin` è lo stesso di `-R` (o di quello predefinito).",
		Example: "  gs pr checkout 7\n  gs pr checkout #7 --json sourceBranch,headSha",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := prArg(c, args)
			if err != nil {
				return err
			}
			e, err := repoenv.New(f)
			if err != nil {
				return err
			}
			if err := checkOrigin(c, f, e); err != nil {
				return err
			}
			resp, err := e.Gen.GetPullRequestWithResponse(c.Context(), e.Owner, e.Repo, n)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			p := resp.JSON200
			if !p.SourceBranchExists {
				return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, ErrCode: "not_found",
					Err: fmt.Errorf("il ramo di origine %s della PR #%d non esiste più: non c'è nulla da scaricare", p.SourceBranch, n)}
			}
			src := p.SourceBranch
			ctx := c.Context()
			if err := f.RunGit(ctx, "fetch", "origin", "+refs/heads/"+src+":refs/remotes/origin/"+src); err != nil {
				return err
			}
			if err := f.RunGit(ctx, "switch", src); err != nil {
				return err
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, prOut{PullRequest: *p, URL: webURL(e, n)}, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Sul ramo %s (PR #%d)\n", src, n)
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, PRFields)
	return cmd
}

// checkOrigin verifica che il remote origin del repo corrente sia il repo
// della PR: fetch e switch lavorano su origin, e con -R su un altro repo
// scaricherebbero il ramo sbagliato.
func checkOrigin(c *cobra.Command, f *cmdutil.Factory, e *repoenv.Env) error {
	if f.Git == nil || f.RunGit == nil {
		return cmdutil.UsageErrorf("checkout richiede un repo git con remote origin")
	}
	r, err := gsrepo.OriginRemote(c.Context(), f.Git)
	if err != nil {
		return cmdutil.UsageErrorf("checkout richiede un repo git con remote origin: sei nel clone di %s/%s?", e.Owner, e.Repo)
	}
	if !strings.EqualFold(r.FullName(), e.Owner+"/"+e.Repo) {
		return cmdutil.UsageErrorf("il remote origin è %s ma la PR è su %s/%s: lancia il comando nel clone giusto", r.FullName(), e.Owner, e.Repo)
	}
	return nil
}
