package issue

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo       output.JSONOptions
		comments bool
		web      bool
	)
	cmd := &cobra.Command{
		Use:   "view <numero>",
		Short: "Mostra una issue",
		Long: "Mostra titolo, stato, etichette, assegnatari, milestone e testo di una issue. I commenti si aggiungono con " +
			"--comments (o chiedendo il campo `comments` con --json). Con --web apre la pagina nel browser.",
		Example: "  gs issue view 12 --comments\n  gs issue view #12 --json title,state,comments\n  gs issue view 12 --web",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
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
			if web {
				u := e.webURL(n)
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Apro %s nel browser\n", u)
				return openURL(u)
			}
			resp, err := e.gen.GetIssueWithResponse(c.Context(), e.owner, e.repo, n)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			is := resp.JSON200
			want := comments
			for _, fld := range jo.Fields {
				if fld == "comments" {
					want = true
				}
			}
			var cs []gitstack.IssueComment
			if want {
				if cs, err = e.allComments(c, n); err != nil {
					return err
				}
			}
			if jo.Enabled() {
				out := issueOut{Issue: *is, URL: e.webURL(n)}
				if want {
					out.Comments = &cs
				}
				return jo.Write(f.IO.Out, out, f.IO.OutTTY)
			}
			return e.renderIssue(is, cs, want)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&comments, "comments", "c", false, "Mostra anche i commenti")
	fl.BoolVarP(&web, "web", "w", false, "Apre la issue nel browser")
	output.AddJSONFlags(cmd, &jo, ViewFields)
	return cmd
}

// allComments legge tutti i commenti della issue (dal più vecchio).
func (e *env) allComments(c *cobra.Command, n int64) ([]gitstack.IssueComment, error) {
	var out []gitstack.IssueComment
	per := 100
	for page := 1; ; page++ {
		pg := page
		resp, err := e.gen.ListIssueCommentsWithResponse(c.Context(), e.owner, e.repo, n, &gitstack.ListIssueCommentsParams{Page: &pg, PerPage: &per})
		if err != nil {
			return nil, err
		}
		if err := e.check(resp.StatusCode(), resp.Body); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, unexpected(resp.StatusCode())
		}
		out = append(out, resp.JSON200.Items...)
		if len(resp.JSON200.Items) == 0 || page*per >= resp.JSON200.Total {
			return out, nil
		}
	}
}

func userNames(us []gitstack.IssueUser) string {
	var ns []string
	for _, u := range us {
		ns = append(ns, string(u.Username))
	}
	return strings.Join(ns, ", ")
}

func (e *env) renderIssue(is *gitstack.Issue, cs []gitstack.IssueComment, withComments bool) error {
	w := e.f.IO.Out
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("#%d %s\n", is.Number, is.Title)
	state := "aperta"
	if is.State == gitstack.IssueState("closed") {
		state = "chiusa"
		if is.CloseReason != nil {
			state += " (" + strings.ReplaceAll(string(*is.CloseReason), "_", " ")
			if is.DuplicateOf != nil {
				state += fmt.Sprintf(" di #%d", *is.DuplicateOf)
			}
			state += ")"
		}
	}
	p("Stato: %s\n", state)
	p("Autore: %s, il %s\n", is.Author.Username, day(is.CreatedAt))
	if len(is.Labels) > 0 {
		var ls []string
		for _, l := range is.Labels {
			ls = append(ls, l.Name)
		}
		p("Etichette: %s\n", strings.Join(ls, ", "))
	}
	if len(is.Assignees) > 0 {
		p("Assegnatari: %s\n", userNames(is.Assignees))
	}
	if is.Milestone != nil {
		p("Milestone: %s (#%d)\n", is.Milestone.Title, is.Milestone.Number)
	}
	if is.Locked {
		p("Discussione bloccata\n")
	}
	p("Commenti: %d\n", is.CommentCount)
	p("URL: %s\n", e.webURL(is.Number))
	if strings.TrimSpace(is.Body) != "" {
		p("\n%s\n", strings.TrimRight(is.Body, "\n"))
	}
	if withComments {
		for _, c := range cs {
			p("\n--\n%s, il %s", c.Author.Username, day(c.CreatedAt))
			if c.Edited {
				p(" (modificato)")
			}
			p("\n")
			if c.Deleted {
				p("[commento eliminato]\n")
			} else {
				p("%s\n", strings.TrimRight(c.Body, "\n"))
			}
		}
	}
	return nil
}
