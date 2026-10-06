package milestone

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newCloseCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "close <numero>",
		Short:   "Chiude una milestone",
		Long: "Chiude una milestone (I7). Le issue aperte restano, ma non vengono " +
			"contate nell'avanzamento. Serve il permesso write.",
		Example: "  gs milestone close 3",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			state := gitstack.UpdateMilestoneInputStateClosed
			in := gitstack.UpdateMilestoneInput{State: &state}
			resp, err := e.gen.UpdateMilestoneWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.MilestoneNumberParam(n), in)
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
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Chiusa milestone #%d: %s\n", resp.JSON200.Number, resp.JSON200.Title)
			}
			return e.printMilestone(&jo, resp.JSON200)
		},
	}
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}

func newReopenCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "reopen <numero>",
		Short:   "Riapre una milestone",
		Long: "Riapre una milestone chiusa (I7). Le issue chiuse come `completed` " +
			"vengono contate nell'avanzamento. Serve il permesso write.",
		Example: "  gs milestone reopen 3",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := numberArg(c, args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			state := gitstack.UpdateMilestoneInputStateOpen
			in := gitstack.UpdateMilestoneInput{State: &state}
			resp, err := e.gen.UpdateMilestoneWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.MilestoneNumberParam(n), in)
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
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Riaperta milestone #%d: %s\n", resp.JSON200.Number, resp.JSON200.Title)
			}
			return e.printMilestone(&jo, resp.JSON200)
		},
	}
	output.AddJSONFlags(cmd, &jo, MilestoneFields)
	return cmd
}
