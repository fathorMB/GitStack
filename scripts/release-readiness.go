//go:build ignore

// release-readiness controlla i criteri di uscita V1 della v1.0 che si possono
// verificare dal repo, e stampa quelli che restano manuali. Si lancia dalla
// radice del repo:
//
//	go run scripts/release-readiness.go [--strict] [--ci] [--root <cartella>]
//
// Un criterio automatico può essere soddisfatto, non ancora soddisfatto
// (con l'elenco di cosa manca) oppure non verificabile (strumento assente).
// Un criterio non ancora soddisfatto NON è un errore dello script:
//   - uscita 0: lo script ha girato (default, anche con criteri aperti);
//   - uscita 1: con --strict, almeno un criterio automatico non è soddisfatto
//     (è la modalità «script verde» da lanciare prima di tagliare la rc);
//   - uscita 2: errore dello script (file della tabella illeggibile, ecc.).
//
// --ci aggiunge l'esito dell'ultimo giro su main dei workflow smoke e install
// tramite la CLI `gh`; senza `gh` o senza rete quei criteri restano manuali.
//
// Fonte dei criteri: docs/release-checklist.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type state int

const (
	met state = iota
	unmet
	unknown
)

type result struct {
	id      string
	title   string
	state   state
	details []string
}

var (
	root   string
	strict bool
	useCI  bool
)

func main() {
	flag.BoolVar(&strict, "strict", false, "esce con 1 se un criterio automatico non è soddisfatto")
	flag.BoolVar(&useCI, "ci", false, "legge con gh l'ultimo esito di smoke e install su main")
	flag.StringVar(&root, "root", ".", "radice del repo")
	flag.Parse()

	var results []result
	var errs []string
	add := func(r result, err error) {
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.id, err))
			return
		}
		results = append(results, r)
	}

	add(checkRulesCoverage())
	add(checkSecurityRegister("A2", "Revisione di sicurezza chiusa: nessun problema aperto nel registro di docs/security/asvs-l2.md", false))
	add(checkSecurityRegister("A3", "Nessun problema critico o alto aperto nel registro di sicurezza", true))
	add(checkFiles("A4", "Test di fumo V3 presente e in CI (smoke.yml, TestCaricoFumo)", []fileNeed{
		{".github/workflows/smoke.yml", "workflow_dispatch"},
		{"services/core/internal/stackitest/smoke_load_integration_test.go", "func TestCaricoFumo("},
	}))
	add(checkFiles("A5", "Installazione, aggiornamento e ripristino in CI su Ubuntu 22.04 e 24.04 (install.yml)", []fileNeed{
		{".github/workflows/install.yml", "ubuntu-22.04"},
		{".github/workflows/install.yml", "ubuntu-24.04"},
		{".github/workflows/install.yml", "Dopo il restore"},
	}))
	add(checkFiles("A6", "Prova dell'agente gs presente (TestAgentTrial e checklist scripts/agent-trial/check.sh)", []fileNeed{
		{"services/core/internal/stackitest/gs_agent_trial_integration_test.go", "func TestAgentTrial("},
		{"scripts/agent-trial/check.sh", "PASS"},
	}))
	add(checkFiles("A7", "Pipeline di rilascio su tag presente (job release di ci.yml, release-changelog.sh)", []fileNeed{
		{".github/workflows/ci.yml", "\n  release:"},
		{"scripts/release-changelog.sh", ""},
	}))
	add(runGoCheck("A8", "SECURITY.md conforme (scripts/check-security-md.go)", "scripts/check-security-md.go"))
	if useCI {
		add(ciRun("A9", "Ultimo giro di smoke su main verde", "smoke.yml"))
		add(ciRun("A10", "Ultimo giro di install su main verde (installazione, aggiornamento, ripristino)", "install.yml"))
	}

	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "release-readiness: errore dello script:")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
		os.Exit(2)
	}

	fmt.Println("Criteri automatici")
	fmt.Println("==================")
	open := 0
	for _, r := range results {
		mark := "[ok]    "
		switch r.state {
		case unmet:
			mark = "[APERTO]"
			open++
		case unknown:
			mark = "[?]     "
		}
		label := map[state]string{met: "soddisfatto", unmet: "non ancora soddisfatto", unknown: "non verificabile qui: da fare a mano"}[r.state]
		fmt.Printf("%s %s %s: %s\n", mark, r.id, r.title, label)
		for _, d := range r.details {
			fmt.Println("           - " + d)
		}
	}

	fmt.Println()
	fmt.Println("Criteri manuali (docs/release-checklist.md)")
	fmt.Println("===========================================")
	manual := []string{
		"M1  M-01…M-08 completate (board: milestone chiuse)",
		"M2  Nessuna issue critica o alta aperta nel tracker (le medie nelle note di rilascio)",
		"M3  Smoke, install, upgrade e restore verdi in CI sull'ultimo commit di main (rilancia con --ci o guarda Actions)",
		"M4  Prova del rilascio su un tag di prova v0.0.0-test.N su un fork, prima di v1.0.0-rc.1 (V4)",
		"M5  Prova reale dell'agente: chiude una issue end-to-end solo con gs e skills (docs/agent-trial.md)",
		"M6  Dogfooding: almeno 4 settimane senza perdite di dati, dalla tappa 2 di V7 (passaggio a GitStack)",
		"M7  Il tag v1.0.0-rc.1 lo crea il board, poi v1.0.0 dopo le rc",
	}
	for _, m := range manual {
		fmt.Println("  [manuale] " + m)
	}

	fmt.Println()
	if open == 0 {
		fmt.Println("Esito: criteri automatici soddisfatti. Restano i manuali qui sopra.")
		return
	}
	fmt.Printf("Esito: %d criteri automatici non ancora soddisfatti (non è un errore dello script).\n", open)
	if strict {
		os.Exit(1)
	}
}

// --- lettura delle tabelle markdown ---------------------------------------

func readLF(rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

func cells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// --- criteri ---------------------------------------------------------------

// checkRulesCoverage: nessuna riga «da compilare» o «non coperta», e la
// tabella coerente (stesso controllo di CI).
func checkRulesCoverage() (result, error) {
	r := result{id: "A1", title: "docs/rules-coverage.md senza righe «da compilare» né «non coperta»"}
	src, err := readLF("docs/rules-coverage.md")
	if err != nil {
		return r, err
	}
	var todo, uncovered []string
	partial := 0
	for _, line := range strings.Split(src, "\n") {
		c := cells(line)
		if len(c) != 5 || c[0] == "Id" || strings.HasPrefix(c[0], "---") {
			continue
		}
		switch c[3] {
		case "da compilare":
			todo = append(todo, c[0])
		case "non coperta":
			uncovered = append(uncovered, c[0])
		case "parziale":
			partial++
		}
	}
	if len(todo) > 0 {
		r.state = unmet
		r.details = append(r.details, "da compilare: "+strings.Join(todo, ", "))
	}
	if len(uncovered) > 0 {
		r.state = unmet
		r.details = append(r.details, "non coperte: "+strings.Join(uncovered, ", "))
	}
	if partial > 0 {
		r.details = append(r.details, fmt.Sprintf("%d righe «parziale»: ognuna dichiara cosa manca nella tabella (non bloccano questo criterio)", partial))
	}
	if r.state == met {
		if res, err := runGoCheck("A1", "", "scripts/check-rules-coverage.go"); err == nil && res.state != met {
			r.state = res.state
			r.details = append(r.details, res.details...)
		}
	}
	return r, nil
}

var resolvedRe = regexp.MustCompile(`(?i)\*\*resolved|\*\*ok\*\*`)

// checkSecurityRegister legge il registro dei problemi di asvs-l2.md: una
// riga F<n> è chiusa se la sua area inizia con «**Resolved» o è «**ok**».
func checkSecurityRegister(id, title string, onlyHigh bool) (result, error) {
	r := result{id: id, title: title}
	src, err := readLF("docs/security/asvs-l2.md")
	if err != nil {
		return r, err
	}
	idx := strings.Index(src, "## Findings register")
	if idx < 0 {
		return r, fmt.Errorf("sezione «Findings register» non trovata in docs/security/asvs-l2.md")
	}
	fRe := regexp.MustCompile(`^F\d+$`)
	rows := 0
	for _, line := range strings.Split(src[idx:], "\n") {
		if strings.HasPrefix(line, "### ") || (rows > 0 && strings.HasPrefix(line, "## ")) {
			break
		}
		c := cells(line)
		if len(c) < 3 || !fRe.MatchString(c[0]) {
			continue
		}
		rows++
		sev := strings.ToLower(strings.Trim(c[1], "* "))
		if onlyHigh && sev != "high" && sev != "critical" {
			continue
		}
		if resolvedRe.MatchString(c[1]) || resolvedRe.MatchString(c[2]) {
			continue
		}
		r.state = unmet
		area := c[2]
		if len(area) > 90 {
			area = area[:90] + "…"
		}
		r.details = append(r.details, fmt.Sprintf("%s (%s) aperto: %s", c[0], sev, area))
	}
	if rows == 0 {
		return r, fmt.Errorf("nessuna riga F<n> nel registro di docs/security/asvs-l2.md")
	}
	return r, nil
}

type fileNeed struct{ path, text string }

func checkFiles(id, title string, needs []fileNeed) (result, error) {
	r := result{id: id, title: title}
	for _, n := range needs {
		src, err := readLF(n.path)
		switch {
		case err != nil:
			r.state = unmet
			r.details = append(r.details, "manca "+n.path)
		case n.text != "" && !strings.Contains(src, n.text):
			r.state = unmet
			r.details = append(r.details, fmt.Sprintf("%s non contiene %q", n.path, strings.TrimSpace(n.text)))
		}
	}
	return r, nil
}

// runGoCheck lancia uno script di controllo del repo. Un fallimento del
// controllo è «non ancora soddisfatto»; l'assenza di go lo rende non
// verificabile.
func runGoCheck(id, title, script string) (result, error) {
	r := result{id: id, title: title}
	goBin, err := exec.LookPath("go")
	if err != nil {
		r.state = unknown
		r.details = append(r.details, "go non trovato nel PATH")
		return r, nil
	}
	cmd := exec.Command(goBin, "run", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.state = unmet
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				r.details = append(r.details, l)
			}
		}
		if len(r.details) > 12 {
			r.details = append(r.details[:12], "…")
		}
	}
	return r, nil
}

// ciRun legge con gh l'ultimo giro del workflow sul ramo main.
func ciRun(id, title, workflow string) (result, error) {
	r := result{id: id, title: title, state: unknown}
	gh, err := exec.LookPath("gh")
	if err != nil {
		r.details = append(r.details, "gh non trovato nel PATH")
		return r, nil
	}
	cmd := exec.Command(gh, "run", "list", "--workflow", workflow, "--branch", "main", "--limit", "1",
		"--json", "conclusion,status,headSha", "--jq", `.[0] | "\(.status) \(.conclusion) \(.headSha)"`)
	cmd.Dir = root
	out, err := cmd.Output()
	line := strings.TrimSpace(string(out))
	if err != nil || line == "" || line == "null" {
		r.details = append(r.details, "gh non ha restituito nessun giro (rete, accesso o workflow mai girato)")
		return r, nil
	}
	f := strings.Fields(line)
	if len(f) >= 2 && f[0] == "completed" && f[1] == "success" {
		r.state = met
		r.details = append(r.details, "commit "+f[len(f)-1])
		return r, nil
	}
	r.state = unmet
	r.details = append(r.details, "ultimo giro su main: "+line)
	return r, nil
}
