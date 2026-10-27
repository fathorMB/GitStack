//go:build ignore

// check-test-paths verifica che i `paths` dei controlli go-test-* di
// .galaxylab/checks.toml coprano tutto quello da cui i loro test dipendono
// davvero (GIT-410), cosi' i paths non si mantengono a mano e un item che tocca
// un solo modulo non fa girare i test degli altri.
//
// Per ogni controllo `go-test-*` (tranne quelli con `cwd`, che testano un solo
// pacchetto con i propri tag) si prendono i pacchetti del suo `run` e si
// ricavano:
//  1. con `go list -deps -test` ogni pacchetto del repo da cui dipendono i test:
//     serve che i paths coprano i suoi file Go, gli embed e il go.mod/go.sum del
//     suo modulo;
//  2. i file non Go letti dai test, cercati nei sorgenti *_test.go dei pacchetti
//     testati: percorsi letterali "../..." che esistono nel repo e coppie di
//     argomenti tipo "deploy", "install.sh" / "scripts", "x.go" (radici deploy,
//     docs, skills, scripts, web, api, README.md);
//  3. go.work e go.work.sum.
//
// Verifica anche che i go-test-* insieme testino ogni modulo di go.work, una
// volta sola.
//
// Si lancia dalla radice del repo:
//
//	go run scripts/check-test-paths.go            # verifica (exit 1 se mancano)
//	go run scripts/check-test-paths.go -suggest   # stampa i paths suggeriti
//
// -suggest da' i paths minimi a granularita' di modulo (<cartella>/**) piu' i
// file non Go: e' il modo per scrivere o aggiornare `paths` in checks.toml.
// Con -checks <file> si legge un altro checks.toml (serve a provare lo script).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type check struct {
	name  string
	run   string
	cwd   string
	paths []string
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "check-test-paths:", err)
	os.Exit(2)
}

func sh(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

var (
	reKV      = regexp.MustCompile(`^(\w+)\s*=\s*(.*)$`)
	reStr     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	reRelLit  = regexp.MustCompile(`"((?:\.\./)+[A-Za-z0-9_./-]+)"`)
	reRootLit = regexp.MustCompile(`"(deploy|docs|skills|scripts|web|api|README\.md)"(?:\s*,\s*"([^"]+)")?`)
)

// parseChecks legge le tabelle [[check]]: solo name, run, cwd e paths (array su
// una riga o su piu' righe), come scritto in checks.toml.
func parseChecks(file string) ([]check, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []check
	var cur *check
	var pending string // chiave di un array aperto
	var arr strings.Builder
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		t := strings.TrimSpace(l)
		if pending != "" {
			arr.WriteString(t)
			if strings.Contains(t, "]") {
				cur.paths = unquote(arr.String())
				pending = ""
			}
			continue
		}
		if t == "[[check]]" {
			flush()
			cur = &check{}
			continue
		}
		if cur == nil || t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := reKV.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		switch m[1] {
		case "name", "run", "cwd":
			s := reStr.FindStringSubmatch(m[2])
			if s == nil {
				continue
			}
			v := strings.ReplaceAll(s[1], `\"`, `"`)
			switch m[1] {
			case "name":
				cur.name = v
			case "run":
				cur.run = v
			case "cwd":
				cur.cwd = v
			}
		case "paths":
			if strings.Contains(m[2], "]") {
				cur.paths = unquote(m[2])
			} else {
				pending = "paths"
				arr.Reset()
				arr.WriteString(m[2])
			}
		}
	}
	flush()
	return out, sc.Err()
}

func unquote(s string) []string {
	var out []string
	for _, m := range reStr.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// globRegexp converte un glob dei paths (** attraversa le cartelle, * no).
func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i++
		case g[i] == '*':
			b.WriteString("[^/]*")
		case g[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(g[i])))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func covered(res []*regexp.Regexp, file string) bool {
	for _, r := range res {
		if r.MatchString(file) {
			return true
		}
	}
	return false
}

type listed struct {
	ImportPath string
	Dir        string
	Module     *struct{ Path, Dir string }
	Files      []string // path relativi alla radice, / come separatore
}

func rel(root, p string) (string, bool) {
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// goList esegue go list sui pattern e ritorna i pacchetti del repo con i loro file.
func goList(root string, args []string, patterns []string) ([]listed, error) {
	full := append([]string{"list", "-e"}, args...)
	full = append(full, "-json=ImportPath,Dir,Module,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,EmbedFiles,TestEmbedFiles,XTestEmbedFiles")
	full = append(full, patterns...)
	out, err := sh("go", full...)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var res []listed
	for dec.More() {
		var p struct {
			ImportPath string
			Dir        string
			Module     *struct{ Path, Dir string }
			GoFiles, CgoFiles, TestGoFiles, XTestGoFiles,
			EmbedFiles, TestEmbedFiles, XTestEmbedFiles []string
		}
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		if p.Dir == "" {
			continue
		}
		dir, ok := rel(root, p.Dir)
		if !ok {
			continue // libreria standard o cache dei moduli
		}
		l := listed{ImportPath: p.ImportPath, Dir: dir, Module: p.Module}
		for _, set := range [][]string{p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles, p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles} {
			for _, f := range set {
				if filepath.IsAbs(f) {
					continue // main di test generato nella cache di build
				}
				l.Files = append(l.Files, filepath.ToSlash(filepath.Join(dir, f)))
			}
		}
		res = append(res, l)
	}
	return res, nil
}

func packagesOf(run string) []string {
	var out []string
	for _, f := range strings.Fields(run) {
		if strings.HasPrefix(f, "./") {
			out = append(out, f)
		}
	}
	return out
}

func isTestCheck(c check) bool {
	return strings.HasPrefix(c.name, "go-test-") && c.cwd == "" && len(packagesOf(c.run)) > 0
}

// required: file (o prefisso "<dir>/*" per le cartelle) -> motivo.
type need struct {
	file   string
	reason string
}

func collect(root string, c check) ([]need, []string, error) {
	pats := packagesOf(c.run)
	deps, err := goList(root, []string{"-deps", "-test"}, pats)
	if err != nil {
		return nil, nil, err
	}
	roots, err := goList(root, nil, pats)
	if err != nil {
		return nil, nil, err
	}
	var needs []need
	seen := map[string]bool{}
	add := func(file, reason string) {
		if !seen[file] {
			seen[file] = true
			needs = append(needs, need{file, reason})
		}
	}
	modDirs := map[string]bool{}
	for _, p := range deps {
		for _, f := range p.Files {
			add(f, "dipendenza "+p.ImportPath)
		}
		if p.Module != nil {
			if md, ok := rel(root, p.Module.Dir); ok {
				modDirs[md] = true
			}
		}
	}
	for md := range modDirs {
		add(md+"/go.mod", "go.mod del modulo "+md)
		add(md+"/go.sum", "go.sum del modulo "+md)
	}
	add("go.work", "workspace")
	add("go.work.sum", "workspace")

	// file non Go letti dai test dei pacchetti testati
	for _, p := range roots {
		for _, f := range p.Files {
			if !strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
			if err != nil {
				return nil, nil, err
			}
			base := p.Dir
			for _, m := range reRelLit.FindAllStringSubmatch(string(src), -1) {
				target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(base), filepath.FromSlash(m[1]))))
				addExisting(root, target, f, add)
			}
			// radici del repo solo nelle righe che costruiscono un percorso e
			// nei file che risalgono con ".." o runtime.Caller
			if !strings.Contains(string(src), `".."`) && !strings.Contains(string(src), "runtime.Caller") {
				continue
			}
			var pathLines []string
			for _, line := range strings.Split(string(src), "\n") {
				if strings.Contains(line, "Join(") || strings.Contains(line, "Abs(") || strings.Contains(line, "ReadFile(") {
					pathLines = append(pathLines, line)
				}
			}
			for _, m := range reRootLit.FindAllStringSubmatch(strings.Join(pathLines, "\n"), -1) {
				target := m[1]
				if m[2] != "" {
					target += "/" + m[2]
				}
				addExisting(root, target, f, add)
			}
		}
	}
	sort.Slice(needs, func(i, j int) bool { return needs[i].file < needs[j].file })
	var mods []string
	for md := range modDirs {
		mods = append(mods, md)
	}
	sort.Strings(mods)
	return needs, mods, nil
}

// addExisting aggiunge un file letto da un test se esiste nel repo (e non e'
// una cartella di moduli Go gia' coperta come dipendenza); per una cartella
// chiede "<dir>/*" (basta un file coperto dai paths).
func addExisting(root, target, from string, add func(string, string)) {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return
	}
	if st.IsDir() {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(target)))
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				add(target+"/"+e.Name(), "letto da "+from)
				return // un file rappresentativo
			}
		}
		return
	}
	add(target, "letto da "+from)
}

// suggest riduce le esigenze a glob: moduli interi e radici non Go.
func suggest(needs []need, mods []string) []string {
	set := map[string]bool{}
	for _, m := range mods {
		set[m+"/**"] = true
	}
	for _, n := range needs {
		if strings.HasPrefix(n.reason, "letto da") {
			if strings.Contains(n.file, "/") {
				d := strings.SplitN(n.file, "/", 2)[0]
				if d == "scripts" {
					set[n.file] = true
				} else {
					set[d+"/**"] = true
				}
			} else {
				set[n.file] = true
			}
		}
	}
	set["go.work"] = true
	set["go.work.sum"] = true
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func main() {
	suggestMode := flag.Bool("suggest", false, "stampa i paths suggeriti invece di verificare")
	checksFile := flag.String("checks", filepath.Join(".galaxylab", "checks.toml"), "checks.toml da leggere")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	checks, err := parseChecks(*checksFile)
	if err != nil {
		fatal(err)
	}
	var tests []check
	for _, c := range checks {
		if isTestCheck(c) {
			tests = append(tests, c)
		}
	}
	if len(tests) == 0 {
		fatal(fmt.Errorf("nessun controllo go-test-* in %s", *checksFile))
	}

	failed := false
	if !*suggestMode {
		// ogni modulo di go.work e' testato da un go-test-* e da uno solo
		out, err := sh("go", "work", "edit", "-json")
		if err != nil {
			fatal(err)
		}
		var w struct{ Use []struct{ DiskPath string } }
		if err := json.Unmarshal([]byte(out), &w); err != nil {
			fatal(err)
		}
		for _, u := range w.Use {
			d := strings.TrimPrefix(filepath.ToSlash(u.DiskPath), "./")
			var by []string
			for _, c := range tests {
				for _, p := range packagesOf(c.run) {
					p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/...")
					if p == d {
						by = append(by, c.name)
					}
				}
			}
			if len(by) != 1 {
				fmt.Printf("KO modulo %s testato da %d controlli go-test-* (%s), ne serve uno\n", d, len(by), strings.Join(by, ", "))
				failed = true
			}
		}
	}

	for _, c := range tests {
		needs, mods, err := collect(root, c)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", c.name, err))
		}
		if *suggestMode {
			fmt.Printf("%s: paths = [", c.name)
			for i, s := range suggest(needs, mods) {
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Printf("%q", s)
			}
			fmt.Println("]")
			continue
		}
		var res []*regexp.Regexp
		for _, g := range c.paths {
			res = append(res, globRegexp(g))
		}
		missing := 0
		for _, n := range needs {
			if !covered(res, n.file) {
				if missing < 15 {
					fmt.Printf("KO %s: i paths non coprono %s (%s)\n", c.name, n.file, n.reason)
				}
				missing++
			}
		}
		if missing > 15 {
			fmt.Printf("KO %s: ... e altri %d file\n", c.name, missing-15)
		}
		if missing > 0 {
			failed = true
		} else {
			fmt.Printf("ok %s: %d file di dipendenze coperti da %d glob\n", c.name, len(needs), len(c.paths))
		}
	}
	if failed {
		fmt.Println("suggerimento: go run scripts/check-test-paths.go -suggest")
		os.Exit(1)
	}
}
