package label

import (
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
		name        string
		newName     string
		color       string
		description string
	)
	cmd := &cobra.Command{
		Use:   "edit <nome>",
		Short: "Modifica un'etichetta",
		Long: "Modifica un'etichetta esistente. Almeno un campo deve essere cambiato. " +
			"Il nuovo nome è unico nel repo senza distinguere maiuscole/minuscole (409 `already_exists`). " +
			"Serve il permesso write.",
		Example: "  gs label edit bug --color ff0000\n  gs label edit bug --name problem",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name = strings.TrimSpace(args[0])
			if name == "" {
				return cmdutil.UsageErrorf("il nome non può essere vuoto")
			}
			if color != "" {
				if err := validateColor(color); err != nil {
					return cmdutil.UsageErrorf("colore: %v", err)
				}
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if !c.Flags().Changed("name") && !c.Flags().Changed("color") && !c.Flags().Changed("description") {
				return cmdutil.UsageErrorf("niente da modificare: indica almeno un flag (--name, --color, --description)")
			}
			in := gitstack.UpdateLabelInput{}
			if c.Flags().Changed("name") {
				newName = strings.TrimSpace(newName)
				if newName == "" {
					return cmdutil.UsageErrorf("il nuovo nome non può essere vuoto")
				}
				in.Name = &newName
			}
			if c.Flags().Changed("color") {
				in.Color = (*gitstack.LabelColor)(&color)
			}
			if c.Flags().Changed("description") {
				in.Description = &description
			}
			resp, err := e.gen.UpdateLabelWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.LabelNameParam(name), in)
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
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Modificata etichetta %s\n", name)
			}
			return e.printLabel(&jo, resp.JSON200)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&newName, "name", "n", "", "Nuovo nome")
	fl.StringVarP(&color, "color", "c", "", "Nuovo colore esadecimale a sei cifre")
	fl.StringVarP(&description, "description", "d", "", "Nuova descrizione")
	output.AddJSONFlags(cmd, &jo, LabelFields)
	return cmd
}
