package issue

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

var dupRe = regexp.MustCompile(`^duplicate(?:[\s_-]*of)?[\s:]*#?(\d+)$`)

// parseCloseReason interpreta il motivo di chiusura (I2): `completed`,
// `not planned` (anche not_planned, not-planned) o `duplicate` con il numero
// dell'altra issue, dato con --duplicate-of o nel motivo (`duplicate #5`).
func parseCloseReason(reason, duplicateOf string) (gitstack.CloseIssueInput, error) {
	var in gitstack.CloseIssueInput
	r := strings.ToLower(strings.TrimSpace(reason))
	var dup int64
	if m := dupRe.FindStringSubmatch(r); m != nil {
		dup, _ = strconv.ParseInt(m[1], 10, 64)
		r = "duplicate"
	}
	if d := strings.TrimPrefix(strings.TrimSpace(duplicateOf), "#"); d != "" {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 1 {
			return in, cmdutil.UsageErrorf("--duplicate-of: serve il numero di una issue (`5` o `#5`), non %q", duplicateOf)
		}
		dup = n
		if r == "" {
			r = "duplicate"
		}
	}
	r = strings.NewReplacer(" ", "_", "-", "_").Replace(r)
	switch r {
	case "", "completed":
		if dup != 0 {
			return in, cmdutil.UsageErrorf("--duplicate-of vale solo con il motivo `duplicate`")
		}
		if r != "" {
			c := gitstack.Completed
			in.Reason = &c
		}
	case "not_planned":
		if dup != 0 {
			return in, cmdutil.UsageErrorf("--duplicate-of vale solo con il motivo `duplicate`")
		}
		c := gitstack.NotPlanned
		in.Reason = &c
	case "duplicate":
		if dup == 0 {
			return in, cmdutil.UsageErrorf("il motivo `duplicate` vuole la issue originale: --duplicate-of <numero> o `--reason \"duplicate #5\"`")
		}
		c := gitstack.Duplicate
		in.Reason = &c
		in.DuplicateOf = &dup
	default:
		return in, cmdutil.UsageErrorf("motivo %q sconosciuto: completed, \"not planned\" o duplicate (con --duplicate-of <numero>)", reason)
	}
	return in, nil
}

// postComment aggiunge un commento (usato da close e reopen con --comment).
func (e *env) postComment(c *cobra.Command, n int64, body string) error {
	resp, err := e.gen.CreateIssueCommentWithResponse(c.Context(), e.owner, e.repo, n, gitstack.CreateIssueCommentInput{Body: body})
	if err != nil {
		return err
	}
	return e.check(resp.StatusCode(), resp.Body)
}

func newCloseCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo        output.JSONOptions
		reason    string
		duplicate string
		comment   string
	)
	cmd := &cobra.Command{
		Use:   "close <numero>",
		Short: "Chiudi una issue con un motivo",
		Long: "Chiude una issue con un motivo (I2): `completed` (predefinito), `not planned` o `duplicate` di un'altra issue " +
			"del repo. Possono chiudere chi ha write e l'autore della issue (I3). Una issue già chiusa risponde con errore (409).",
		Example: "  gs issue close 12\n  gs issue close 12 --reason \"not planned\" --comment \"fuori dal perimetro\"\n" +
			"  gs issue close 12 --reason duplicate --duplicate-of 7\n  gs issue close 12 -r \"duplicate #7\"",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			in, err := parseCloseReason(reason, duplicate)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if comment != "" {
				if err := e.postComment(c, n, comment); err != nil {
					return err
				}
			}
			resp, err := e.gen.CloseIssueWithResponse(c.Context(), e.owner, e.repo, n, in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printIssue(&jo, resp.JSON200)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&reason, "reason", "r", "", "Motivo: completed, \"not planned\" o duplicate (anche `duplicate #n`)")
	fl.StringVarP(&duplicate, "duplicate-of", "d", "", "Con duplicate: numero della issue originale")
	fl.StringVarP(&comment, "comment", "c", "", "Lascia prima un commento")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}

func newReopenCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo      output.JSONOptions
		comment string
	)
	cmd := &cobra.Command{
		Use:   "reopen <numero>",
		Short: "Riapri una issue",
		Long: "Riapre una issue chiusa e azzera il motivo di chiusura (I2). Possono riaprire chi ha write e l'autore (I3). " +
			"Una issue già aperta risponde con errore (409).",
		Example: "  gs issue reopen 12 --comment \"si ripresenta\"",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if comment != "" {
				if err := e.postComment(c, n, comment); err != nil {
					return err
				}
			}
			resp, err := e.gen.ReopenIssueWithResponse(c.Context(), e.owner, e.repo, n)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return e.printIssue(&jo, resp.JSON200)
		},
	}
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "Lascia prima un commento")
	output.AddJSONFlags(cmd, &jo, IssueFields)
	return cmd
}
