//go:build ignore

// check-internal-versions verifica che le immagini dei servizi usino le
// sorgenti LOCALI dei moduli interni (github.com/fathorMB/GitStack/pkg/*,
// client/go) e non la pseudo-versione scritta nel loro go.mod (GIT-184, dopo
// GIT-182).
//
// Perche': cosi' un item che cambia un pkg/* e' un commit solo, senza bump dei
// go.mod, e l'immagine contiene comunque il codice del commit. Il nome del
// file e del check (go-internal-versions) resta quello di GIT-182.
//
// Per ogni servizio (modulo del workspace con un Dockerfile nella cartella):
//  1. il Dockerfile COPIA da contesto la cartella di ogni modulo interno che
//     il go.mod richiede (anche come antenato, es. "COPY pkg ./pkg");
//  2. il Dockerfile usa scripts/docker-local-replace.sh e ogni RUN con
//     "go mod download" o "go build" lo ha prima (stesso RUN o RUN precedente);
//  3. ogni voce dei workflow .github/workflows/*.yml che costruisce quel
//     Dockerfile ha per contesto la radice (context: . nella matrix,
//     "docker build -f <Dockerfile> ... ." negli script);
//  4. nel Makefile (sviluppo locale: make dev-up, dev-redeploy) CONTEXT_<svc>
//     di quel servizio, se definita, vale ".".
//
// I moduli si ricavano da `go work edit -json`; i moduli senza Dockerfile (cli,
// admin, ...) si costruiscono nel workspace e sono saltati. Si lancia dalla
// radice del repo:
//
//	go run scripts/check-internal-versions.go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	internalPrefix = "github.com/fathorMB/GitStack/"
	replaceScript  = "docker-local-replace.sh"
)

func run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "check-internal-versions:", err)
	os.Exit(2)
}

// instruction e' un'istruzione di Dockerfile con le continuazioni unite.
type instruction struct {
	op   string // in maiuscolo
	args string
}

func readDockerfile(path string) ([]instruction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []instruction
	var cur string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		t := strings.TrimSpace(l)
		if cur == "" && (t == "" || strings.HasPrefix(t, "#")) {
			continue
		}
		if strings.HasSuffix(l, "\\") {
			cur += strings.TrimSuffix(l, "\\") + " "
			continue
		}
		cur += l
		fields := strings.Fields(cur)
		cur = ""
		if len(fields) == 0 {
			continue
		}
		out = append(out, instruction{
			op:   strings.ToUpper(fields[0]),
			args: strings.Join(fields[1:], " "),
		})
	}
	return out, sc.Err()
}

// covers dice se la sorgente COPY src copre la cartella dir (src uguale o antenato).
func covers(src, dir string) bool {
	src = strings.Trim(filepath.ToSlash(src), "/")
	src = strings.TrimPrefix(src, "./")
	if src == "." || src == "" {
		return true
	}
	return dir == src || strings.HasPrefix(dir, src+"/")
}

// checkDockerfile ritorna i problemi di un Dockerfile di servizio.
func checkDockerfile(path string, needDirs []string) []string {
	ins, err := readDockerfile(path)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	var srcs []string
	for _, i := range ins {
		if i.op != "COPY" {
			continue
		}
		var args []string
		from := false
		for _, a := range strings.Fields(i.args) {
			if strings.HasPrefix(a, "--from") {
				from = true
			}
			if !strings.HasPrefix(a, "--") {
				args = append(args, a)
			}
		}
		if from || len(args) < 2 {
			continue
		}
		srcs = append(srcs, args[:len(args)-1]...)
	}
	for _, d := range needDirs {
		ok := false
		for _, s := range srcs {
			if covers(s, d) {
				ok = true
				break
			}
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("il go.mod richiede il modulo interno %s ma nessuna COPY ne porta le sorgenti nell'immagine", d))
		}
	}
	// Lo script dei replace deve essere nell'immagine e girare prima di
	// "go mod download" e "go build".
	copiesScript := false
	for _, s := range srcs {
		if covers(s, "scripts/"+replaceScript) {
			copiesScript = true
		}
	}
	if !copiesScript {
		problems = append(problems, "nessuna COPY di scripts/"+replaceScript)
	}
	replaced := false
	for _, i := range ins {
		if i.op != "RUN" {
			continue
		}
		// Posizione del replace e dei comandi Go dentro la stessa riga RUN.
		r := strings.Index(i.args, replaceScript)
		g := -1
		for _, kw := range []string{"go mod download", "go build"} {
			if p := strings.Index(i.args, kw); p >= 0 && (g < 0 || p < g) {
				g = p
			}
		}
		if g >= 0 && !replaced && (r < 0 || r > g) {
			problems = append(problems, "un RUN con go mod download/go build gira senza i replace verso le sorgenti locali ("+replaceScript+" prima)")
		}
		if r >= 0 {
			replaced = true
		}
	}
	if !replaced {
		problems = append(problems, "nessun RUN usa "+replaceScript+": la build prenderebbe i moduli interni dalle pseudo-versioni")
	}
	return problems
}

var dockerBuildRe = regexp.MustCompile(`docker\s+build\b`)

// checkWorkflow cerca nei workflow le build dei Dockerfile dei servizi.
func checkWorkflow(path string, dockerfiles []string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{err.Error()}
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var problems []string
	lastContext := ""
	lastContextLine := 0
	// Statement di shell con le continuazioni "\" unite.
	var stmt string
	stmtLine := 0
	flush := func() {
		if stmt == "" {
			return
		}
		if dockerBuildRe.MatchString(stmt) {
			for _, df := range dockerfiles {
				if !strings.Contains(stmt, df) {
					continue
				}
				fields := strings.Fields(stmt)
				ctx := strings.Trim(fields[len(fields)-1], `"'`)
				if ctx != "." {
					problems = append(problems, fmt.Sprintf("%s:%d: docker build di %s con contesto %q invece della radice (.)", path, stmtLine, df, ctx))
				}
			}
		}
		stmt = ""
	}
	for n, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "context:") {
			lastContext = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "context:")), `"'`)
			lastContextLine = n + 1
		}
		if strings.HasPrefix(t, "dockerfile:") {
			df := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "dockerfile:")), `"'`)
			for _, d := range dockerfiles {
				if df == d && lastContext != "." {
					problems = append(problems, fmt.Sprintf("%s:%d: matrix di %s con context %q (riga %d) invece della radice (.)", path, n+1, d, lastContext, lastContextLine))
				}
			}
		}
		if stmt == "" {
			stmtLine = n + 1
		}
		if strings.HasSuffix(t, "\\") {
			stmt += strings.TrimSuffix(t, "\\") + " "
			continue
		}
		stmt += t
		flush()
	}
	flush()
	return problems
}

var makeContextRe = regexp.MustCompile(`^CONTEXT_([A-Za-z0-9_]+)\s*[:?]?=\s*(\S*)`)

// checkMakefile controlla che CONTEXT_<svc> di ogni servizio con Dockerfile
// (cartella services/<svc>) sia la radice.
func checkMakefile(path string, dockerfiles []string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{err.Error()}
	}
	svcs := map[string]string{}
	for _, df := range dockerfiles {
		svcs[filepath.Base(filepath.Dir(filepath.FromSlash(df)))] = df
	}
	var problems []string
	for n, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		m := makeContextRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if df, ok := svcs[m[1]]; ok && m[2] != "." {
			problems = append(problems, fmt.Sprintf("%s:%d: CONTEXT_%s := %s invece della radice (.) per %s", path, n+1, m[1], m[2], df))
		}
	}
	return problems
}

func main() {
	out, err := run("go", "work", "edit", "-json")
	if err != nil {
		fatal(fmt.Errorf("go work edit -json (si lancia dalla radice del repo): %w", err))
	}
	var work struct {
		Use []struct{ DiskPath string }
	}
	if err := json.Unmarshal(out, &work); err != nil {
		fatal(err)
	}
	if len(work.Use) == 0 {
		fatal(fmt.Errorf("nessun modulo in go.work"))
	}

	type goMod struct {
		Module  struct{ Path string }
		Require []struct{ Path string }
	}
	dirOf := map[string]string{} // path modulo -> cartella
	parsed := map[string]goMod{}
	var order []string
	for _, u := range work.Use {
		dir := filepath.ToSlash(filepath.Clean(u.DiskPath))
		o, err := run("go", "mod", "edit", "-json", filepath.Join(dir, "go.mod"))
		if err != nil {
			fatal(err)
		}
		var m goMod
		if err := json.Unmarshal(o, &m); err != nil {
			fatal(err)
		}
		dirOf[m.Module.Path] = dir
		parsed[m.Module.Path] = m
		order = append(order, m.Module.Path)
	}

	failed := 0
	var dockerfiles []string
	for _, mp := range order {
		dir := dirOf[mp]
		df := dir + "/Dockerfile"
		if _, err := os.Stat(filepath.FromSlash(df)); err != nil {
			fmt.Printf("skip %s: nessun Dockerfile, si costruisce nel workspace\n", dir)
			continue
		}
		dockerfiles = append(dockerfiles, df)
		var need []string
		for _, r := range parsed[mp].Require {
			if d, ok := dirOf[r.Path]; ok && strings.HasPrefix(r.Path, internalPrefix) {
				need = append(need, d)
			}
		}
		if ps := checkDockerfile(filepath.FromSlash(df), need); len(ps) > 0 {
			for _, p := range ps {
				fmt.Printf("FAIL %s: %s\n", df, p)
			}
			failed++
		} else {
			fmt.Printf("ok   %s: sorgenti locali di %v\n", df, need)
		}
	}
	if len(dockerfiles) == 0 {
		fatal(fmt.Errorf("nessun modulo del workspace ha un Dockerfile"))
	}
	wfs, _ := filepath.Glob(filepath.Join(".github", "workflows", "*.yml"))
	for _, wf := range wfs {
		for _, p := range checkWorkflow(filepath.ToSlash(wf), dockerfiles) {
			fmt.Println("FAIL", p)
			failed++
		}
	}
	for _, p := range checkMakefile("Makefile", dockerfiles) {
		fmt.Println("FAIL", p)
		failed++
	}
	if failed > 0 {
		fmt.Printf("check-internal-versions: %d problemi: le immagini non usano le sorgenti locali dei moduli interni\n", failed)
		os.Exit(1)
	}
	fmt.Printf("check-internal-versions: %d Dockerfile con sorgenti locali, workflow e Makefile con contesto radice\n", len(dockerfiles))
}
