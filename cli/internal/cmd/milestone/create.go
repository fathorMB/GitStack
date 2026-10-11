package milestone

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo          output.JSONOptions
		title       string
		description string
		dueOn       string
	)
	cmd := &cobra.Command{
		Use:   "create <titolo>",
		Short: "Crea una milestone",
		Long: "Crea una milestone (I7). Il titolo è obbligatorio, 1-256 caratteri. " +
			"La descrizione è facoltativa (max 4096 caratteri). La data di scadenza " +
			"è opzionale (formato AAAA-MM-GG). Serve il permesso write.",
		Example: "  gs milestone create v1.0\n  gs milestone create v1.0 -d \"Rilascio 1.0\" --due-on 2026-12-01",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			title = strings.TrimSpace(args[0])
			if title == "" {
				return cmdutil.UsageErrorf("il titolo non può essere vuoto")
			}
			var dueOnParsed *openapi_types.Date
			if dueOn != "" {
				parsed, err := parseDate(dueOn)
				if err != nil {
					return cmdutil.UsageErrorf("data scadenza: %v", err)
				}
				dueOnParsed = &parsed
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			in := gitstack.CreateMilestoneInput{
				Title: title,
			}
			if description != "" {
				in.Description = &description
			}
			if dueOnParsed != nil {
				in.DueOn = dueOnParsed
			}
			resp, err := e.gen.CreateMilestoneWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			if !jo.Enabled() {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Creata milestone #%d: %s\n", resp.JSON201.Number, title)
			}
			return e.printMilestone(&jo, resp.JSON201)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&title, "title", "t", "", "Titolo della milestone (obbligatorio)")
	fl.StringVarP(&description, "description", "d", "", "Descrizione della milestone")
	fl.StringVarP(&dueOn, "due-on", "D", "", "Data di scadenza (formato AAAA-MM-GG)")
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}
