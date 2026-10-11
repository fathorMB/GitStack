package issue

import (
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newEditCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo              output.JSONOptions
		title           string
		body            string
		bodyFile        string
		addLabels       []string
		removeLabels    []string
		addAssignees    []string
		removeAssignees []string
		milestone       string
		removeMilestone bool
	)
	cmd := &cobra.Command{
		Use:   "edit <numero>",
		Short: "Modifica titolo, testo, etichette, assegnatari e milestone",
		Long: "Modifica una issue. Titolo e testo li cambia solo l'autore (I4); etichette, assegnatari e milestone chiedono write. " +
			"Le etichette e gli assegnatari si aggiungono o tolgono rispetto a quelli attuali.",
		Example: "  gs issue edit 12 --title \"Nuovo titolo\" --add-label bug --remove-label wontfix\n" +
			"  gs issue edit 12 --add-assignee @me --milestone v1\n" +
			"  gs issue edit 12 --body-file nuovo.md",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			text, bodySet, err := readBody(f, body, c.Flags().Changed("body"), bodyFile)
			if err != nil {
				return err
			}
			titleSet := c.Flags().Changed("title")
			if titleSet && title == "" {
				return cmdutil.UsageErrorf("il titolo non può essere vuoto")
			}
			if milestone != "" && removeMilestone {
				return cmdutil.UsageErrorf("--milestone e --remove-milestone sono alternativi")
			}
			labelsChange := len(addLabels)+len(removeLabels) > 0
			assigneesChange := len(addAssignees)+len(removeAssignees) > 0
			if !titleSet && !bodySet && !labelsChange && !assigneesChange && milestone == "" && !removeMilestone {
				return cmdutil.UsageErrorf("niente da modificare: indica almeno un flag (--title, --body, --add-label, ...)")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			ctx := c.Context()
			var last *gitstack.Issue

			if titleSet || bodySet {
				in := gitstack.UpdateIssueInput{}
				if titleSet {
					in.Title = &title
				}
				if bodySet {
					in.Body = &text
				}
				resp, err := e.gen.UpdateIssueWithResponse(ctx, e.owner, e.repo, n, in)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				last = resp.JSON200
			}

			var cur *gitstack.Issue
			if labelsChange || assigneesChange {
				resp, err := e.gen.GetIssueWithResponse(ctx, e.owner, e.repo, n)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				cur = resp.JSON200
			}

			if labelsChange {
				var names []string
				for _, l := range cur.Labels {
					names = append(names, l.Name)
				}
				names = apply(names, addLabels, removeLabels)
				if names == nil {
					names = []string{}
				}
				resp, err := e.gen.SetIssueLabelsWithResponse(ctx, e.owner, e.repo, n, gitstack.SetIssueLabelsInput{Labels: names})
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				last = resp.JSON200
			}

			if assigneesChange {
				add, err := e.users(ctx, addAssignees)
				if err != nil {
					return err
				}
				rem, err := e.users(ctx, removeAssignees)
				if err != nil {
					return err
				}
				var names []string
				for _, u := range cur.Assignees {
					names = append(names, string(u.Username))
				}
				names = apply(names, add, rem)
				if names == nil {
					names = []string{}
				}
				resp, err := e.gen.SetIssueAssigneesWithResponse(ctx, e.owner, e.repo, n, gitstack.SetIssueAssigneesInput{Assignees: names})
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				last = resp.JSON200
			}

			if milestone != "" || removeMilestone {
				in := gitstack.SetIssueMilestoneInput{}
				if milestone != "" {
					m, err := e.milestoneNumber(ctx, milestone)
					if err != nil {
						return err
					}
					in.Milestone = &m
				}
				resp, err := e.gen.SetIssueMilestoneWithResponse(ctx, e.owner, e.repo, n, in)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				last = resp.JSON200
			}
			return e.printIssue(&jo, last)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&title, "title", "t", "", "Nuovo titolo")
	fl.StringVarP(&body, "body", "b", "", "Nuovo testo (Markdown)")
	fl.StringVarP(&bodyFile, "body-file", "F", "", "Legge il nuovo testo da un file (`-` per stdin)")
	fl.StringSliceVar(&addLabels, "add-label", nil, "Etichetta da aggiungere (ripetibile)")
	fl.StringSliceVar(&removeLabels, "remove-label", nil, "Etichetta da togliere (ripetibile)")
	fl.StringSliceVar(&addAssignees, "add-assignee", nil, "Assegnatario da aggiungere (`@me` per te)")
	fl.StringSliceVar(&removeAssignees, "remove-assignee", nil, "Assegnatario da togliere")
	fl.StringVarP(&milestone, "milestone", "m", "", "Milestone, per numero o titolo")
	fl.BoolVar(&removeMilestone, "remove-milestone", false, "Toglie la milestone")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}

// apply parte da cur, aggiunge add e toglie rem, senza doppioni e in ordine.
func apply(cur, add, rem []string) []string {
	drop := map[string]bool{}
	for _, r := range dedupe(rem) {
		drop[r] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, cur...), dedupe(add)...) {
		if !drop[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
