// Package skills è il gruppo `gs skills` (M-07/I): installa e aggiorna le
// skills per gli agenti di coding (formato Agent Skills) e mantiene la
// sezione delimitata in AGENTS.md.
//
// Le skills arrivano dall'istanza: <host>/downloads/index.json dà lo sha256 di
// gs-skills.zip, che si scarica e si verifica prima di scrivere qualsiasi file.
// Destinazioni (documentate in skills/README.md):
//
//	Claude Code  .claude/skills/<nome>/  (progetto)  ~/.claude/skills/<nome>/  (utente)
//	Codex        .agents/skills/<nome>/  (progetto)  ~/.agents/skills/<nome>/  (utente)
package skills

import (
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs skills`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Installa le skills per gli agenti di coding",
		Long: "Installa e aggiorna le skills di GitStack (formato Agent Skills) per gli agenti di coding. Le skills si scaricano " +
			"dall'istanza (/downloads/gs-skills.zip, verificate con lo sha256 di index.json).\n\n" +
			destinationsHelp,
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newInstallCmd(f), newUpdateCmd(f))
	return cmd
}

const destinationsHelp = "Cartelle di destinazione:\n" +
	"  Claude Code  .claude/skills/<nome>/ nel progetto, ~/.claude/skills/<nome>/ per l'utente\n" +
	"  Codex        .agents/skills/<nome>/ nel progetto, ~/.agents/skills/<nome>/ per l'utente\n" +
	"Un agente è riconosciuto dalla presenza di .claude/ (Claude Code) o .codex/ o .agents/ (Codex) nel progetto o nella home; " +
	"--agent lo sceglie a mano. Il progetto è la radice del repo git in cui sei."
