package issue

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// printIssue scrive l'esito di un comando che restituisce una issue: JSON con
// i campi richiesti, altrimenti l'indirizzo web su stdout.
func (e *env) printIssue(jo *output.JSONOptions, is *gitstack.Issue) error {
	if jo.Enabled() {
		return jo.Write(e.f.IO.Out, issueOut{Issue: *is, URL: e.webURL(is.Number)}, e.f.IO.OutTTY)
	}
	_, err := fmt.Fprintln(e.f.IO.Out, e.webURL(is.Number))
	return err
}

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo        output.JSONOptions
		title     string
		body      string
		bodyFile  string
		labels    []string
		assignees []string
		milestone string
		template  string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Apri una issue",
		Long: "Apre una issue nel repo. Il testo viene da --body, da --body-file (`-` legge stdin) o da un modello di " +
			".gitstack/ISSUE_TEMPLATE/ (--template, I11). Etichette, assegnatari e milestone richiedono il permesso write.\n\n" +
			"Su stdout stampa l'indirizzo della issue (o il JSON con --json).",
		Example: "  gs issue create --title \"Crash all'avvio\" --body-file crash.md --label bug --assignee @me\n" +
			"  echo \"dettagli\" | gs issue create -t \"Titolo\" -F -\n" +
			"  gs issue create --template bug -t \"Errore 500 su /login\"",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			text, bodySet, err := readBody(f, body, c.Flags().Changed("body"), bodyFile)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			ctx := c.Context()
			in := gitstack.CreateIssueInput{Title: strings.TrimSpace(title)}
			lbls := append([]string{}, labels...)
			if template != "" {
				tpl, err := e.template(c, template)
				if err != nil {
					return err
				}
				if in.Title == "" && tpl.Title != nil {
					in.Title = *tpl.Title
				}
				if !bodySet {
					text, bodySet = tpl.Body, true
				}
				if tpl.Labels != nil {
					lbls = append(*tpl.Labels, lbls...)
				}
			}
			if in.Title == "" {
				return cmdutil.UsageErrorf("serve il titolo: usa --title")
			}
			if bodySet {
				in.Body = &text
			}
			if l := dedupe(lbls); len(l) > 0 {
				in.Labels = &l
			}
			if len(assignees) > 0 {
				users, err := e.users(ctx, assignees)
				if err != nil {
					return err
				}
				if len(users) > 0 {
					in.Assignees = &users
				}
			}
			if milestone != "" {
				n, err := e.milestoneNumber(ctx, milestone)
				if err != nil {
					return err
				}
				in.Milestone = &n
			}
			resp, err := e.gen.CreateIssueWithResponse(ctx, e.owner, e.repo, in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printIssue(&jo, resp.JSON201)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&title, "title", "t", "", "Titolo della issue")
	fl.StringVarP(&body, "body", "b", "", "Testo (Markdown)")
	fl.StringVarP(&bodyFile, "body-file", "F", "", "Legge il testo da un file (`-` per stdin)")
	fl.StringSliceVarP(&labels, "label", "l", nil, "Etichetta da assegnare (ripetibile, o separate da virgola)")
	fl.StringSliceVarP(&assignees, "assignee", "a", nil, "Assegnatario (ripetibile; `@me` per te)")
	fl.StringVarP(&milestone, "milestone", "m", "", "Milestone, per numero o titolo")
	fl.StringVarP(&template, "template", "T", "", "Modello di .gitstack/ISSUE_TEMPLATE/ (nome del file senza .md)")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}

// template cerca un modello del repo per nome; se non c'è, l'errore elenca
// quelli disponibili.
func (e *env) template(c *cobra.Command, name string) (*gitstack.IssueTemplate, error) {
	resp, err := e.gen.ListIssueTemplatesWithResponse(c.Context(), e.owner, e.repo)
	if err != nil {
		return nil, err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	var names []string
	for i, t := range resp.JSON200.Items {
		if strings.EqualFold(t.Name, strings.TrimSuffix(name, ".md")) {
			return &resp.JSON200.Items[i], nil
		}
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return nil, cmdutil.UsageErrorf("modello %q non trovato: %s/%s non ha modelli in .gitstack/ISSUE_TEMPLATE/", name, e.owner, e.repo)
	}
	return nil, cmdutil.UsageErrorf("modello %q non trovato; disponibili: %s", name, strings.Join(names, ", "))
}

func dedupe(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
