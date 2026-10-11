package skills

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

type flags struct {
	agents   []string
	user     bool
	agentsMD bool
	force    bool
}

func addFlags(cmd *cobra.Command, fl *flags) {
	cmd.Flags().StringSliceVar(&fl.agents, "agent", nil, "Agente di destinazione: "+strings.Join(agentNames(), ", ")+" (ripetibile; senza, quelli riconosciuti)")
	cmd.Flags().BoolVar(&fl.user, "user", false, "Livello utente (home) invece del progetto (radice del repo git)")
	cmd.Flags().BoolVar(&fl.agentsMD, "agents-md", false, "Aggiunge o aggiorna la sezione delimitata di AGENTS.md (solo progetto)")
}

func newInstallCmd(f *cmdutil.Factory) *cobra.Command {
	var fl flags
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Installa le skills dall'istanza per gli agenti presenti",
		Long: "Scarica le skills dall'istanza (/downloads/gs-skills.zip), ne verifica lo sha256 contro index.json e le installa " +
			"per gli agenti riconosciuti, nel progetto (radice del repo git) o, con --user, nella home. In ogni cartella scrive " +
			VersionFile + ", che `gs skills update` confronta con l'istanza. Una cartella esistente non installata da gs " +
			"non si tocca senza --force.\n\n" + destinationsHelp,
		Example: "  gs skills install\n  gs skills install --agent codex --user\n  gs skills install --agents-md",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runInstall(c, f, fl, false)
		},
	}
	addFlags(cmd, &fl)
	cmd.Flags().BoolVar(&fl.force, "force", false, "Sovrascrive anche cartelle non installate da gs")
	return cmd
}

func newUpdateCmd(f *cmdutil.Factory) *cobra.Command {
	var fl flags
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Allinea le skills installate alla versione dell'istanza",
		Long: "Per ogni agente con skills installate da gs (nel progetto, o con --user nella home) confronta lo sha256 in " +
			VersionFile + " con quello di index.json dell'istanza e, se differisce, le reinstalla. Con --agents-md aggiorna anche " +
			"la sezione di AGENTS.md.",
		Example: "  gs skills update\n  gs skills update --user",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runInstall(c, f, fl, true)
		},
	}
	addFlags(cmd, &fl)
	return cmd
}

func runInstall(c *cobra.Command, f *cmdutil.Factory, fl flags, update bool) error {
	if fl.agentsMD && fl.user {
		return cmdutil.UsageErrorf("--agents-md vale solo per il progetto: toglilo o togli --user")
	}
	sc, err := resolveScope(f, fl.user)
	if err != nil {
		return err
	}
	var targets []target
	if update {
		targets, err = sc.updateTargets(fl.agents)
		if err == nil && len(targets) == 0 {
			return cmdutil.UsageErrorf("nessuna skill installata da gs in %s: usa `gs skills install`", sc.base)
		}
	} else {
		targets, err = sc.installTargets(fl.agents)
	}
	if err != nil {
		return err
	}
	host, err := f.Host()
	if err != nil {
		return err
	}
	b, err := fetchBundle(c.Context(), f.HTTPClient(), host)
	if err != nil {
		return err
	}
	out := f.IO.Out

	if !fl.force {
		for _, t := range targets {
			if cf := conflicts(t, b); len(cf) > 0 {
				return fmt.Errorf("%s esiste già e non è stata installata da gs: usa --force per sovrascrivere", strings.Join(cf, ", "))
			}
		}
	}
	for _, t := range targets {
		if update && upToDate(t, b) {
			_, _ = fmt.Fprintf(out, "%s: già aggiornato (%s)\n", t.Agent.Label, shortSum(b.SHA256))
			continue
		}
		written, err := writeSkills(t, b, host)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Agent.Label, err)
		}
		verb := "installate"
		if update {
			verb = "aggiornate"
		}
		_, _ = fmt.Fprintf(out, "%s: %d skills %s in %s\n", t.Agent.Label, len(written), verb, rel(t.Dir, sc))
	}
	if fl.agentsMD {
		p, changed, err := writeAgentsMD(sc.base, b.Names)
		if err != nil {
			return err
		}
		if changed {
			_, _ = fmt.Fprintf(out, "AGENTS.md aggiornato (%s)\n", rel(p, sc))
		} else {
			_, _ = fmt.Fprintf(out, "AGENTS.md già aggiornato (%s)\n", rel(p, sc))
		}
	}
	return nil
}

// upToDate è vero se tutte le skills del pacchetto sono installate con lo
// stesso sha256.
func upToDate(t target, b *bundle) bool {
	inst := installedSkills(t.Dir)
	for _, n := range b.Names {
		m, ok := inst[n]
		if !ok || m.SHA256 != b.SHA256 {
			return false
		}
	}
	return true
}

func shortSum(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func rel(p string, sc *scope) string {
	if r, err := filepath.Rel(sc.base, p); err == nil && !strings.HasPrefix(r, "..") {
		if sc.user {
			return filepath.Join("~", r)
		}
		return r
	}
	return p
}
