package label

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo          output.JSONOptions
		name        string
		color       string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create <nome>",
		Short: "Crea un'etichetta",
		Long: "Crea un'etichetta (I5, I3). Il nome è unico nel repo senza distinguere " +
			"maiuscole/minuscole (409 `already_exists`), 1-50 caratteri, senza `/`, " +
			"virgole o caratteri di controllo. Il colore è esadecimale a sei cifre. " +
			"Serve il permesso write.",
		Example: "  gs label create bug --color d73a4a\n  gs label create enhancement --color a2eeef",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			name = strings.TrimSpace(args[0])
			if name == "" {
				return cmdutil.UsageErrorf("il nome non può essere vuoto")
			}
			if err := validateColor(color); err != nil {
				return cmdutil.UsageErrorf("colore: %v", err)
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			in := gitstack.CreateLabelInput{
				Name:  name,
				Color: gitstack.LabelColor(color),
			}
			if description != "" {
				in.Description = &description
			}
			resp, err := e.gen.CreateLabelWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), in)
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
				_, _ = fmt.Fprintf(f.IO.ErrOut, "Creata etichetta %s\n", name)
			}
			return e.printLabel(&jo, resp.JSON201)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&color, "color", "c", "", "Colore esadecimale a sei cifre (es. d73a4a)")
	fl.StringVarP(&description, "description", "d", "", "Descrizione dell'etichetta")
	output.AddJSONFlags(cmd, &jo, LabelFields)
	return cmd
}
