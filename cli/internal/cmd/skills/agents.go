package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// Agent è un agente di coding con la sua cartella delle skills, relativa alla
// radice del progetto o alla home.
type Agent struct {
	Name      string   // valore di --agent
	Label     string   // nome per le persone
	Markers   []string // cartelle che ne rivelano la presenza
	SkillsDir string   // cartella delle skills (con "/")
}

// Agents sono gli agenti supportati. Fonti ufficiali delle cartelle:
// https://code.claude.com/docs/en/skills (.claude/skills, ~/.claude/skills) e
// https://developers.openai.com/codex/skills (.agents/skills, $HOME/.agents/skills).
var Agents = []Agent{
	{Name: "claude", Label: "Claude Code", Markers: []string{".claude"}, SkillsDir: ".claude/skills"},
	{Name: "codex", Label: "Codex", Markers: []string{".codex", ".agents"}, SkillsDir: ".agents/skills"},
}

func agentNames() []string {
	var n []string
	for _, a := range Agents {
		n = append(n, a.Name)
	}
	return n
}

// parseAgents risolve i valori di --agent.
func parseAgents(names []string) ([]Agent, error) {
	var out []Agent
	seen := map[string]bool{}
	for _, n := range names {
		for _, part := range strings.Split(n, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" || seen[part] {
				continue
			}
			found := false
			for _, a := range Agents {
				if a.Name == part {
					out = append(out, a)
					seen[part] = true
					found = true
				}
			}
			if !found {
				return nil, cmdutil.UsageErrorf("--agent sconosciuto %q; ammessi: %s", part, strings.Join(agentNames(), ", "))
			}
		}
	}
	return out, nil
}

// detectAgents dà gli agenti con una cartella marcatore in una delle basi.
func detectAgents(bases ...string) []Agent {
	var out []Agent
	for _, a := range Agents {
		if present(a, bases) {
			out = append(out, a)
		}
	}
	return out
}

func present(a Agent, bases []string) bool {
	for _, b := range bases {
		if b == "" {
			continue
		}
		for _, m := range a.Markers {
			if st, err := os.Stat(filepath.Join(b, m)); err == nil && st.IsDir() {
				return true
			}
		}
	}
	return false
}

// homeDir è la home dell'utente: HOME, su Windows USERPROFILE.
func homeDir(getenv func(string) string) (string, error) {
	keys := []string{"HOME"}
	if runtime.GOOS == "windows" {
		keys = []string{"USERPROFILE", "HOME"}
	}
	for _, k := range keys {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v, nil
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dell'utente non determinabile: %w", err)
	}
	return h, nil
}

// projectRoot è la radice del repo git che contiene dir (cartella o file .git).
func projectRoot(dir string) (string, error) {
	d := dir
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", errors.New("non sei in un repo git")
		}
		d = parent
	}
}

// target è una cartella delle skills di un agente.
type target struct {
	Agent Agent
	Dir   string // cartella che contiene <nome>/SKILL.md
	Base  string // radice del progetto o home
}

// scope risolve dove si lavora: la base (progetto o home) e la home.
type scope struct {
	base string
	home string
	user bool
}

func resolveScope(f *cmdutil.Factory, user bool) (*scope, error) {
	home, err := homeDir(f.Getenv)
	if err != nil {
		return nil, err
	}
	if user {
		return &scope{base: home, home: home, user: true}, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := projectRoot(wd)
	if err != nil {
		return nil, cmdutil.UsageErrorf("%v: lancia il comando nel repo del progetto oppure usa --user per installare a livello utente", err)
	}
	return &scope{base: root, home: home}, nil
}

func (s *scope) targetFor(a Agent) target {
	return target{Agent: a, Base: s.base, Dir: filepath.Join(s.base, filepath.FromSlash(a.SkillsDir))}
}

// installTargets sceglie gli agenti: --agent, altrimenti quelli presenti nella
// base o nella home.
func (s *scope) installTargets(names []string) ([]target, error) {
	agents, err := parseAgents(names)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		agents = detectAgents(s.base, s.home)
	}
	if len(agents) == 0 {
		return nil, cmdutil.UsageErrorf("nessun agente riconosciuto (cercate .claude/, .codex/ o .agents/ nel progetto e in home): usa --agent %s", strings.Join(agentNames(), "|"))
	}
	var ts []target
	for _, a := range agents {
		ts = append(ts, s.targetFor(a))
	}
	return ts, nil
}

// updateTargets sceglie le cartelle in cui ci sono skills installate da gs.
func (s *scope) updateTargets(names []string) ([]target, error) {
	agents, err := parseAgents(names)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		agents = Agents
	}
	var ts []target
	for _, a := range agents {
		t := s.targetFor(a)
		if len(installedSkills(t.Dir)) > 0 {
			ts = append(ts, t)
		}
	}
	return ts, nil
}
