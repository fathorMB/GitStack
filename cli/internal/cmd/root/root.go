// Package root assembla il comando radice di gs e ne esegue il ciclo di vita
// (codici di uscita, errori in JSON).
package root

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
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
	"github.com/fathorMB/GitStack/cli/internal/compat"
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
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			return checkCompat(c, f)
		},
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

// compatSkip sono i comandi che non toccano il server o che servono proprio
// a rimettersi in sesto: niente controllo di versione (G6).
var compatSkip = map[string]bool{
	"version": true, "help": true, "completion": true, "__complete": true,
	"auth login": true, "auth logout": true, "auth setup-git": true, "auth git-credential": true,
}

// compatTimeout limita la richiesta a /meta: il controllo non deve rallentare i comandi.
const compatTimeout = 5 * time.Second

// checkCompat confronta la versione di gs con quella del server dell'istanza
// risolta (G6): versione diversa = avviso su stderr, maggiore diversa = errore.
// Se l'istanza non si risolve il comando stesso darà l'errore giusto.
func checkCompat(c *cobra.Command, f *cmdutil.Factory) error {
	path := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	if c == c.Root() || compatSkip[path] {
		return nil
	}
	host, err := f.Host()
	if err != nil {
		return nil
	}
	cache := ""
	if cfg, err := f.Config(); err == nil {
		cache = filepath.Join(cfg.Path(), "compat.json")
	}
	hc := &http.Client{Timeout: compatTimeout}
	if base := f.HTTPClient(); base != nil {
		hc.Transport = base.Transport
	}
	return compat.Check(c.Context(), hc, api.BaseURL(host), f.Version, cache, f.IO.ErrOut)
}
