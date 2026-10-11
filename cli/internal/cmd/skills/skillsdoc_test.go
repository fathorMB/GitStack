package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// skillsDir è la cartella skills/ del monorepo, vista dal pacchetto.
const skillsDir = "../../../../skills"

var expectedSkills = []string{
	"gitstack-issue-to-commit",
	"gitstack-issue-triage",
	"gitstack-notifications",
	"gitstack-repo-admin",
	"gitstack-setup",
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// frontmatter separa il frontmatter YAML (campi `chiave: valore` su una riga)
// dal corpo.
func frontmatter(t *testing.T, src string) (map[string]string, string) {
	t.Helper()
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		t.Fatal("manca il frontmatter (---) in testa")
	}
	rest := src[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatal("frontmatter non chiuso")
	}
	fm := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("riga di frontmatter non valida: %q", line)
		}
		fm[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return fm, rest[end+5:]
}

func readSkill(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(skillsDir, name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillMdValidi(t *testing.T) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	if strings.Join(dirs, ",") != strings.Join(expectedSkills, ",") {
		t.Fatalf("cartelle di skills/ = %v, attese %v", dirs, expectedSkills)
	}
	for _, name := range dirs {
		t.Run(name, func(t *testing.T) {
			fm, body := frontmatter(t, readSkill(t, name))
			if fm["name"] != name {
				t.Errorf("name = %q, atteso il nome della cartella %q", fm["name"], name)
			}
			if !nameRe.MatchString(fm["name"]) || len(fm["name"]) > 64 {
				t.Errorf("name %q: minuscolo con trattini, al più 64 caratteri", fm["name"])
			}
			if d := fm["description"]; d == "" || len(d) > 1024 {
				t.Errorf("description vuota o oltre 1024 caratteri (%d)", len(d))
			}
			if len(strings.TrimSpace(body)) < 200 {
				t.Error("istruzioni troppo corte")
			}
			// G8: ogni skill vieta --yes senza istruzione esplicita di una persona.
			if !strings.Contains(body, "--yes") || !strings.Contains(body, "istruzione esplicita di una persona") {
				t.Error("manca il divieto di --yes senza istruzione esplicita di una persona (G8)")
			}
		})
	}
}

func TestLicenzaEReadme(t *testing.T) {
	lic, err := os.ReadFile(filepath.Join(skillsDir, "LICENSE"))
	if err != nil || !strings.Contains(string(lic), "Apache License") {
		t.Errorf("skills/LICENSE non è Apache-2.0: %v", err)
	}
	rd, err := os.ReadFile(filepath.Join(skillsDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range expectedSkills {
		if !strings.Contains(string(rd), n) {
			t.Errorf("README.md non cita %s", n)
		}
	}
	for _, d := range []string{".claude/skills", "~/.claude/skills", ".agents/skills", "~/.agents/skills"} {
		if !strings.Contains(string(rd), d) {
			t.Errorf("README.md non documenta la destinazione %s", d)
		}
	}
}

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

func newRoot(t *testing.T) (*cobra.Command, func(args ...string) int) {
	t.Helper()
	mk := func() *cmdutil.Factory {
		io_, _, _, _ := cmdutil.Test()
		f := cmdutil.New("test", io_)
		dir := t.TempDir()
		f.Getenv = func(k string) string {
			if k == "GS_CONFIG_DIR" {
				return dir
			}
			return ""
		}
		f.Git = noGit{}
		return f
	}
	return root.NewCmd(mk()), func(args ...string) int {
		return root.Run(context.Background(), mk(), args)
	}
}

// codeBlocks dà le righe dei blocchi di codice recintati (```).
func codeBlocks(md string) []string {
	var lines []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			in = !in
			continue
		}
		if in {
			lines = append(lines, l)
		}
	}
	return lines
}

// tokenize divide una riga di shell per spazi, tenendo insieme le stringhe
// tra apici.
func tokenize(line string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out
}

// ghCommand è un comando gs citato: parole del comando e flag usati.
type ghCommand struct {
	words []string
	flags []string
	line  string
}

var wordRe = regexp.MustCompile(`^[a-z][a-z-]*$`)

// extractGS estrae i comandi `gs ...` dai blocchi di codice, anche dopo una pipe.
func extractGS(md string) []ghCommand {
	var cmds []ghCommand
	for _, line := range codeBlocks(md) {
		toks := tokenize(line)
		for i := 0; i < len(toks); i++ {
			if toks[i] != "gs" || (i > 0 && toks[i-1] != "|" && toks[i-1] != "&&") {
				continue
			}
			c := ghCommand{line: strings.TrimSpace(line)}
			j := i + 1
			for ; j < len(toks) && wordRe.MatchString(toks[j]); j++ {
				c.words = append(c.words, toks[j])
			}
			// i flag: dopo le parole, fino alla prossima pipe.
			for ; j < len(toks) && toks[j] != "|" && toks[j] != "&&"; j++ {
				if strings.HasPrefix(toks[j], "--") {
					c.flags = append(c.flags, strings.SplitN(toks[j], "=", 2)[0])
				}
			}
			cmds = append(cmds, c)
		}
	}
	return cmds
}

func TestComandiCitatiEsistono(t *testing.T) {
	cmd, run := newRoot(t)
	seen := map[string]bool{}
	total := 0
	for _, name := range expectedSkills {
		cmds := extractGS(readSkill(t, name))
		if len(cmds) == 0 {
			t.Errorf("%s: nessun comando gs nei blocchi di codice", name)
		}
		for _, c := range cmds {
			total++
			if len(c.words) == 0 {
				t.Errorf("%s: %q: nessun sottocomando", name, c.line)
				continue
			}
			found, rest, err := cmd.Find(c.words)
			if err != nil || found == cmd {
				t.Errorf("%s: %q: comando inesistente", name, c.line)
				continue
			}
			// un gruppo con un argomento in più è un sottocomando sbagliato
			if found.HasSubCommands() && len(rest) > 0 {
				t.Errorf("%s: %q: %q non è un sottocomando di `%s`", name, c.line, rest[0], found.CommandPath())
				continue
			}
			for _, fl := range c.flags {
				fl = strings.TrimPrefix(fl, "--")
				if found.Flags().Lookup(fl) == nil && found.InheritedFlags().Lookup(fl) == nil {
					t.Errorf("%s: %q: flag --%s inesistente per `%s`", name, c.line, fl, found.CommandPath())
				}
			}
			path := found.CommandPath()
			if seen[path] {
				continue
			}
			seen[path] = true
			args := append(strings.Fields(strings.TrimPrefix(path, cmd.Name()+" ")), "--help")
			if code := run(args...); code != 0 {
				t.Errorf("%s: `gs %s` esce con %d", name, strings.Join(args, " "), code)
			}
		}
	}
	if total < 25 {
		t.Errorf("estratti solo %d comandi: l'estrattore non funziona?", total)
	}
}

func TestEstrattoreComandi(t *testing.T) {
	md := "testo gs finto\n```\nexport A=1\necho \"$T\" | gs auth login --hostname h --with-token\ngs issue comment 12 --body \"a --finto\"\ngit commit -m \"x\"\n```\n"
	got := extractGS(md)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if strings.Join(got[0].words, " ") != "auth login" || strings.Join(got[0].flags, ",") != "--hostname,--with-token" {
		t.Errorf("%+v", got[0])
	}
	if strings.Join(got[1].words, " ") != "issue comment" || strings.Join(got[1].flags, ",") != "--body" {
		t.Errorf("%+v", got[1])
	}
}
