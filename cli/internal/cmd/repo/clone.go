package repo

import (
	"context"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// runGit esegue git con i flussi dati; nei test si sostituisce.
var runGit = func(ctx context.Context, in io.Reader, out, errOut io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	return cmd.Run()
}

func newCloneCmd(f *cmdutil.Factory) *cobra.Command {
	var protocol string
	cmd := &cobra.Command{
		Use:   "clone <owner>/<repo> [<cartella>] [-- <opzioni di git>]",
		Short: "Clona un repository",
		Long: "Clona un repo con git. L'indirizzo è quello dell'installazione (R1, R7): HTTPS di default, SSH se lo scegli con " +
			"--protocol ssh o se l'istanza ha `git_protocol: ssh` nella configurazione (`ssh://git@<host>:2222/<owner>/<repo>.git`, " +
			"porta SSH dell'installazione). Con HTTPS l'accesso usa il token via `gs auth setup-git`; con SSH la chiave registrata sull'account. " +
			"Quello che segue `--` va a `git clone`.",
		Example: "  gs repo clone acme/web\n  gs repo clone acme/web web-locale --protocol ssh\n  gs repo clone acme/web -- --depth 1",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			pos := args
			var extra []string
			if i := c.ArgsLenAtDash(); i >= 0 {
				pos, extra = args[:i], args[i:]
			}
			if len(pos) < 1 || len(pos) > 2 {
				return cmdutil.UsageErrorf("uso: gs repo clone <owner>/<repo> [<cartella>] [-- <opzioni di git>]")
			}
			owner, name, err := parseTarget(pos[0])
			if err != nil {
				return err
			}
			protocol = strings.ToLower(strings.TrimSpace(protocol))
			if protocol != "" && protocol != "https" && protocol != "ssh" {
				return cmdutil.UsageErrorf("--protocol: https o ssh (non %q)", protocol)
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if protocol == "" {
				protocol = e.configuredProtocol()
			}
			r, err := e.get(c.Context(), owner, name)
			if err != nil {
				return err
			}
			url := r.CloneUrls.Https
			if protocol == "ssh" {
				switch {
				case r.CloneUrls.Ssh != nil:
					url = *r.CloneUrls.Ssh
				case r.CloneUrls.SshShort != nil:
					url = *r.CloneUrls.SshShort
				default:
					return cmdutil.UsageErrorf("l'istanza non offre SSH per %s: usa HTTPS", r.FullName)
				}
			}
			gitArgs := []string{"clone", url}
			if len(pos) == 2 {
				gitArgs = append(gitArgs, pos[1])
			}
			gitArgs = append(gitArgs, extra...)
			if err := runGit(c.Context(), f.IO.In, f.IO.Out, f.IO.ErrOut, gitArgs...); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "https o ssh (predefinito: `git_protocol` dell'istanza, altrimenti https)")
	return cmd
}

// configuredProtocol è il git_protocol dell'istanza nella configurazione
// (`ssh`), altrimenti https.
func (e *env) configuredProtocol() string {
	cfg, err := e.f.Config()
	if err != nil {
		return "https"
	}
	hc, ok, err := cfg.Host(e.host)
	if err != nil || !ok || !strings.EqualFold(hc.GitProtocol, "ssh") {
		return "https"
	}
	return "ssh"
}
