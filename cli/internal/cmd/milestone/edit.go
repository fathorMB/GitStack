package milestone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newEditCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo          output.JSONOptions
		title       string
		description string
		dueOn       string
	)
	cmd := &cobra.Command{
		Use:   "edit <numero>",
		Short: "Modifica una milestone",
		Long: "Modifica una milestone esistente. Almeno un campo deve essere cambiato. " +
			"Il titolo è 1-256 caratteri. La descrizione è facoltativa. " +
			"La data di scadenza può essere cambiata o tolta (--remove-due-on). " +
			"Serve il permesso write.",
		Example: "  gs milestone edit 3 --title \"Versione 1.0\" --due-on 2026-12-01\n  gs milestone edit 3 --remove-due-on",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			number, err := numberArg(c, args)
			if err != nil {
				return err
			}
			titleSet := c.Flags().Changed("title")
			descSet := c.Flags().Changed("description")
			dueOnSet := c.Flags().Changed("due-on")
			removeSet := c.Flags().Changed("remove-due-on")
			if titleSet && title == "" {
				return cmdutil.UsageErrorf("il titolo non può essere vuoto")
			}
			if !titleSet && !descSet && !dueOnSet && !removeSet {
				return cmdutil.UsageErrorf("niente da modificare: indica almeno un flag (--title, --description, --due-on, --remove-due-on)")
			}
			if dueOnSet && removeSet {
				return cmdutil.UsageErrorf("--due-on e --remove-due-on sono alternativi")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			// Il corpo è una mappa e non UpdateMilestoneInput: il tipo generato
			// ha dueOn con omitempty e non può mandare `"dueOn": null`, che è
			// come l'API toglie la data (I7).
			in := map[string]any{}
			if titleSet {
				title = strings.TrimSpace(title)
				if title == "" {
					return cmdutil.UsageErrorf("il titolo non può essere vuoto")
				}
				in["title"] = title
			}
			if descSet {
				in["description"] = description
			}
			if dueOnSet {
				if _, err := parseDate(dueOn); err != nil {
					return cmdutil.UsageErrorf("data scadenza: %v", err)
				}
				in["dueOn"] = dueOn
			}
			if removeSet {
				in["dueOn"] = nil
			}
			body, err := json.Marshal(in)
			if err != nil {
				return err
			}
			resp, err := e.gen.UpdateMilestoneWithBodyWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.MilestoneNumberParam(number), "application/json", bytes.NewReader(body))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if !jo.Enabled() {
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Modificata milestone #%d\n", number)
			}
			return e.printMilestone(&jo, resp.JSON200)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&title, "title", "t", "", "Nuovo titolo")
	fl.StringVarP(&description, "description", "d", "", "Nuova descrizione")
	fl.StringVarP(&dueOn, "due-on", "D", "", "Nuova data di scadenza (formato AAAA-MM-GG)")
	fl.Bool("remove-due-on", false, "Toglie la data di scadenza")
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}
