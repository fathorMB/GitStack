// Package releasegate prova scripts/release-readiness.go (GIT-251): il binario
// è costruito dallo script e lanciato con --root su un registro di sicurezza di
// prova, così i test non dipendono dallo stato vero di docs/security/asvs-l2.md.
package releasegate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const registerHead = `# ASVS

## Findings register

| Id | Severity | Area and requirements | Files | Test |
|---|---|---|---|---|
`

const rulesTable = `| Id | Regola | Test | Stato | Cosa manca |
|---|---|---|---|---|
`

// run costruisce lo script e lo lancia su una radice di prova con il registro
// dato; restituisce l'output standard e il codice di uscita.
func run(t *testing.T, register string, args ...string) (string, int) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "release-readiness.go"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "rr")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, script).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := t.TempDir()
	for rel, body := range map[string]string{
		"docs/rules-coverage.md":   rulesTable,
		"docs/security/asvs-l2.md": register,
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, append([]string{"--root", root}, args...)...)
	out, err := cmd.Output()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func lineWith(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, " "+id+" ") {
			return l
		}
	}
	return ""
}

func TestReleaseReadiness_A2SoddisfattoConMediEBassiAperti(t *testing.T) {
	reg := registerHead +
		"| F1 | high | **Resolved (GIT-227)**: scalata | `a.go` | `t` |\n" +
		"| F2 | high | **Resolved (GIT-228).** Segreti | `a.go` | `t` |\n" +
		"| F3 | medium | Intestazioni web (V9.1.2) | `b.go` | `t` |\n" +
		"| F4 | **ok** | Cache | `c.go` | `t` |\n" +
		"| F12 | low | Sessioni (V3.3.2) | `d.go` | `t` |\n" +
		"| F13 | low | **Resolved (GIT-1).** Notifiche | `d.go` | `t` |\n"
	out, _ := run(t, reg)
	if l := lineWith(out, "A2"); !strings.HasPrefix(l, "[ok]") {
		t.Fatalf("A2 doveva essere soddisfatto, riga: %q\n%s", l, out)
	}
	if strings.Contains(out, " A3 ") {
		t.Errorf("A3 non esiste più (assorbito da A2):\n%s", out)
	}
	_, after, found := strings.Cut(out, "Rischi noti da mettere nelle note di rilascio")
	if !found {
		t.Fatalf("manca la sezione dei rischi noti:\n%s", out)
	}
	risks, _, _ := strings.Cut(after, "Criteri manuali")
	for _, want := range []string{"F3 (medium)", "F12 (low)"} {
		if !strings.Contains(risks, want) {
			t.Errorf("rischio noto %q mancante:\n%s", want, risks)
		}
	}
	for _, not := range []string{"F1 ", "F2 ", "F4 ", "F13"} {
		if strings.Contains(risks, "- "+not) {
			t.Errorf("%q risolto non doveva essere fra i rischi noti:\n%s", not, risks)
		}
	}
}

func TestReleaseReadiness_A2FallisceConCriticoOAltoAperto(t *testing.T) {
	for _, sev := range []string{"high", "critical"} {
		t.Run(sev, func(t *testing.T) {
			reg := registerHead +
				"| F1 | " + sev + " | Escalation aperta | `a.go` | `t` |\n" +
				"| F3 | medium | Intestazioni web | `b.go` | `t` |\n"
			out, code := run(t, reg, "--strict")
			if l := lineWith(out, "A2"); !strings.HasPrefix(l, "[APERTO]") {
				t.Fatalf("A2 doveva essere aperto, riga: %q\n%s", l, out)
			}
			if !strings.Contains(out, "F1 ("+sev+") aperto") {
				t.Errorf("il problema aperto non è elencato:\n%s", out)
			}
			if code != 1 {
				t.Errorf("con --strict l'uscita doveva essere 1, è %d", code)
			}
			// F3 aperto resta un rischio noto anche quando A2 fallisce.
			if !strings.Contains(out, "F3 (medium)") {
				t.Errorf("F3 doveva comparire fra i rischi noti:\n%s", out)
			}
		})
	}
}

func TestReleaseReadiness_NessunRischioNoto(t *testing.T) {
	reg := registerHead + "| F1 | high | **Resolved (GIT-1)**: ok | `a.go` | `t` |\n"
	out, _ := run(t, reg)
	if !strings.Contains(out, "nessuno: nel registro di sicurezza non resta aperto niente") {
		t.Errorf("atteso «nessuno» nei rischi noti:\n%s", out)
	}
}
