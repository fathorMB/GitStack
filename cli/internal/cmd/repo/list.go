package repo

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo         output.JSONOptions
		limit      int
		visibility string
		archived   bool
		active     bool
		deleted    bool
	)
	cmd := &cobra.Command{
		Use:     "list [<owner>]",
		Aliases: []string{"ls"},
		Short:   "Elenca i repository",
		Long: "Elenca i repository che puoi leggere, anche di un solo utente o organizzazione. " +
			"Con --deleted elenca quelli eliminati di cui sei proprietario o admin, ancora recuperabili con `gs repo restore` (R2, 7 giorni).",
		Example: "  gs repo list\n  gs repo list acme --visibility internal\n  gs repo list --archived --json fullName,archivedAt\n  gs repo list --deleted",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return cmdutil.UsageErrorf("al più un proprietario: gs repo list [<owner>]")
			}
			if limit < 1 {
				return cmdutil.UsageErrorf("--limit deve essere almeno 1")
			}
			if archived && active {
				return cmdutil.UsageErrorf("--archived e --no-archived sono alternativi")
			}
			var wantVis gitstack.RepoVisibility
			if visibility != "" {
				switch strings.ToLower(visibility) {
				case "private", "internal":
					wantVis = gitstack.RepoVisibility(strings.ToLower(visibility))
				default:
					return cmdutil.UsageErrorf("--visibility: private o internal (non %q)", visibility)
				}
			}
			if deleted && (visibility != "" || archived || active) {
				return cmdutil.UsageErrorf("--deleted non si combina con --visibility, --archived e --no-archived")
			}
			var owner *string
			if len(args) == 1 {
				o := strings.TrimPrefix(strings.TrimSpace(args[0]), "@")
				owner = &o
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if deleted {
				return e.listDeleted(c, &jo, owner, limit)
			}
			per := limit
			if per > 100 {
				per = 100
			}
			var items []gitstack.Repository
			for page := 1; len(items) < limit; page++ {
				pg := page
				resp, err := e.gen.ListRepositoriesWithResponse(c.Context(), &gitstack.ListRepositoriesParams{Owner: owner, Page: &pg, PerPage: &per})
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				for _, r := range resp.JSON200.Items {
					if wantVis != "" && r.Visibility != wantVis {
						continue
					}
					if archived && !r.Archived || active && r.Archived {
						continue
					}
					items = append(items, r)
				}
				if len(resp.JSON200.Items) == 0 || page*per >= resp.JSON200.Total {
					break
				}
			}
			if len(items) > limit {
				items = items[:limit]
			}
			if jo.Enabled() {
				out := make([]repoOut, len(items))
				for i, r := range items {
					out[i] = repoOut{Repository: r, URL: e.webURL(string(r.Owner.Name), r.Name)}
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintln(f.IO.ErrOut, "Nessun repository con questi filtri")
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "REPO", "VISIBILITÀ", "STATO", "AGGIORNATO", "DESCRIZIONE")
			for _, r := range items {
				state := "attivo"
				if r.Archived {
					state = "archiviato"
				}
				upd := ""
				if r.UpdatedAt != nil {
					upd = day(*r.UpdatedAt)
				}
				t.AddRow(r.FullName, string(r.Visibility), state, upd, r.Description)
			}
			return t.Render(f.IO.Out)
		},
	}
	fl := cmd.Flags()
	fl.IntVarP(&limit, "limit", "L", 30, "Numero massimo di repo")
	fl.StringVar(&visibility, "visibility", "", "Solo repo private o internal")
	fl.BoolVar(&archived, "archived", false, "Solo repo archiviati")
	fl.BoolVar(&active, "no-archived", false, "Solo repo non archiviati")
	fl.BoolVar(&deleted, "deleted", false, "Elenca i repo eliminati e ancora recuperabili")
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

func (e *env) listDeleted(c *cobra.Command, jo *output.JSONOptions, owner *string, limit int) error {
	resp, err := e.gen.ListDeletedRepositoriesWithResponse(c.Context(), &gitstack.ListDeletedRepositoriesParams{Owner: owner})
	if err != nil {
		return err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return unexpected(resp.StatusCode())
	}
	items := resp.JSON200.Items
	if len(items) > limit {
		items = items[:limit]
	}
	f := e.f
	if jo.Enabled() {
		out := make([]deletedOut, len(items))
		for i, d := range items {
			o := string(d.Owner.Name)
			out[i] = deletedOut{DeletedRepository: d, FullName: o + "/" + d.Name, URL: e.webURL(o, d.Name)}
		}
		return jo.Write(f.IO.Out, out, f.IO.OutTTY)
	}
	if len(items) == 0 {
		if f.IO.OutTTY {
			_, _ = fmt.Fprintln(f.IO.ErrOut, "Nessun repository eliminato da recuperare")
		}
		return nil
	}
	t := output.NewTable(f.IO.OutTTY, f.IO.Width, "REPO", "ELIMINATO", "CANCELLAZIONE DEFINITIVA")
	for _, d := range items {
		t.AddRow(string(d.Owner.Name)+"/"+d.Name, day(d.DeletedAt), day(d.PurgeAt))
	}
	return t.Render(f.IO.Out)
}
