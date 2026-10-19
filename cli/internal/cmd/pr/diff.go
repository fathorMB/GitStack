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

func newDiffCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo       output.JSONOptions
		nameOnly bool
		ignoreWS bool
	)
	cmd := &cobra.Command{
		Use:   "diff <numero>",
		Short: "Mostra il diff di una Pull Request",
		Long: "Mostra i file modificati dalla PR e il loro patch, dal merge-base alla punta del ramo di origine " +
			"(per una PR unita, il diff congelato al merge). `--name-only` elenca solo i file. I limiti di B6 possono " +
			"omettere alcuni patch: `gs` lo dice su stderr.",
		Example: "  gs pr diff 7\n  gs pr diff 7 --name-only\n  gs pr diff 7 -w --json path,status,additions,deletions",
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
			var p gitstack.ListPullRequestFilesParams
			if ignoreWS {
				t := true
				p.IgnoreWhitespace = &t
			}
			resp, err := e.Gen.ListPullRequestFilesWithResponse(c.Context(), e.Owner, e.Repo, n, &p)
			if err != nil {
				return err
			}
			if err := check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			d := resp.JSON200
			if jo.Enabled() {
				return jo.Write(f.IO.Out, filesOut{PullRequestFiles: *d, URL: webURL(e, n)}, f.IO.OutTTY)
			}
			if d.FilesChanged > len(d.Files) {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Mostrati %d file su %d: il resto non è nell'elenco\n", len(d.Files), d.FilesChanged)
			}
			if d.Truncated || (d.ListOnly != nil && *d.ListOnly) {
				_, _ = fmt.Fprintln(f.IO.ErrOut, "Diff parziale: alcuni patch sono omessi dai limiti (B6)")
			}
			if nameOnly {
				for _, fd := range d.Files {
					_, _ = fmt.Fprintln(f.IO.Out, fd.Path)
				}
				return nil
			}
			for _, fd := range d.Files {
				switch {
				case fd.Patch != nil:
					p := *fd.Patch
					_, _ = fmt.Fprint(f.IO.Out, p)
					if !strings.HasSuffix(p, "\n") {
						_, _ = fmt.Fprintln(f.IO.Out)
					}
				case fd.Binary:
					_, _ = fmt.Fprintf(f.IO.Out, "Binary file %s\n", fd.Path)
				default:
					reason := "patch omesso"
					if fd.CollapseReason != nil {
						reason = string(*fd.CollapseReason)
					}
					_, _ = fmt.Fprintf(f.IO.ErrOut, "nessun patch per %s (%s)\n", fd.Path, reason)
				}
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&nameOnly, "name-only", false, "Elenca solo i file modificati")
	fl.BoolVarP(&ignoreWS, "ignore-whitespace", "w", false, "Ignora le differenze di spazi (git diff -w)")
	output.AddJSONFlags(cmd, &jo, FilesFields)
	return cmd
}
