// Package root assembla il comando radice di gs e ne esegue il ciclo di vita
// (codici di uscita, errori in JSON).
package root

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	apicmd "github.com/fathorMB/GitStack/cli/internal/cmd/api"
	"github.com/fathorMB/GitStack/cli/internal/cmd/auth"
	"github.com/fathorMB/GitStack/cli/internal/cmd/blame"
	"github.com/fathorMB/GitStack/cli/internal/cmd/browse"
	"github.com/fathorMB/GitStack/cli/internal/cmd/issue"
	"github.com/fathorMB/GitStack/cli/internal/cmd/label"
	"github.com/fathorMB/GitStack/cli/internal/cmd/milestone"
	"github.com/fathorMB/GitStack/cli/internal/cmd/notification"
	"github.com/fathorMB/GitStack/cli/internal/cmd/repo"
	"github.com/fathorMB/GitStack/cli/internal/cmd/search"
	"github.com/fathorMB/GitStack/cli/internal/cmd/skills"
	"github.com/fathorMB/GitStack/cli/internal/cmd/version"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// NewCmd costruisce `gs` con tutti i gruppi della v1 (G4). Un gruppo nuovo si
// aggiunge qui con una riga; i sottocomandi stanno nelle cartelle dei gruppi.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gs <comando> <sottocomando> [flag]",
		Short:         "gs, la CLI di GitStack",
		Long:          "gs, la CLI di GitStack per persone e agenti di coding (stile gh).",
		Version:       f.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          cmdutil.GroupRun,
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cmdutil.UsageErrorf("comando sconosciuto %q per %q", args[0], c.CommandPath())
			}
			return nil
		},
	}
	cmd.SetOut(f.IO.Out)
	cmd.SetErr(f.IO.ErrOut)
	cmd.SetIn(f.IO.In)
	cmd.SetVersionTemplate(version.Line(f.Version) + "\n")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &cmdutil.UsageError{Msg: err.Error()}
	})
	cmd.PersistentFlags().StringVar(&f.HostnameFlag, "hostname", "", "Istanza di GitStack (host[:porta]) invece di quella del remote o predefinita")
	cmd.PersistentFlags().StringVarP(&f.RepoFlag, "repo", "R", "", "Repository `owner/repo` invece di quello del remote origin")

	cmd.AddCommand(
		auth.NewCmd(f),
		repo.NewCmd(f),
		issue.NewCmd(f),
		label.NewCmd(f),
		milestone.NewCmd(f),
		search.NewCmd(f),
		browse.NewCmd(f),
		blame.NewCmd(f),
		notification.NewCmd(f),
		apicmd.NewCmd(f),
		skills.NewCmd(f),
		version.NewCmd(f),
	)
	return cmd
}

// Run esegue gs con gli argomenti dati e restituisce il codice di uscita.
// In modalità JSON (--json o --jq) l'errore va su stderr come
// {"error":{"code","message"}}, altrimenti come testo.
func Run(ctx context.Context, f *cmdutil.Factory, args []string) int {
	cmd := NewCmd(f)
	cmd.SetArgs(args)
	executed, err := cmd.ExecuteContextC(ctx)
	if err == nil || errors.Is(err, cmdutil.ErrSilent) {
		return cmdutil.ExitOK
	}
	if jsonMode(executed) {
		output.WriteError(f.IO.ErrOut, err)
	} else {
		_, _ = fmt.Fprintf(f.IO.ErrOut, "gs: %v\n", err)
		var ue *cmdutil.UsageError
		if errors.As(err, &ue) && executed != nil {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Usa `%s --help` per l'uso.\n", executed.CommandPath())
		}
	}
	return cmdutil.ExitCode(err)
}

func jsonMode(c *cobra.Command) bool {
	if c == nil {
		return false
	}
	for _, name := range []string{"json", "jq"} {
		if fl := c.Flags().Lookup(name); fl != nil && fl.Changed {
			return true
		}
	}
	return false
}
