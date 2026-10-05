//go:build ignore

// check-rules-coverage verifica docs/rules-coverage.md: ogni test citato nella
// tabella deve esistere ancora nel file indicato, e le righe devono essere
// coerenti (stato noto, test per le righe coperte/parziali, «cosa manca» per
// parziali e non coperte). Si lancia dalla radice del repo:
//
//	go run scripts/check-rules-coverage.go [percorso-tabella]
//
// Riferimenti ai test, tra apici inversi: `percorso#Nome`.
//   - Go (.go): Nome è TestNome oppure TestNome/sottotest/...; si cerca
//     `func TestNome(` nel file e la stringa "sottotest" tra virgolette.
//   - Altri file (web): Nome è il titolo del test, o un suo pezzo: si cerca
//     la stringa nel file.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	refRe    = regexp.MustCompile("`([^`]+)`")
	statuses = map[string]bool{
		"coperta": true, "parziale": true, "non coperta": true,
		"n/a, motivata": true, "da compilare": true,
	}
)

func main() {
	table := "docs/rules-coverage.md"
	if len(os.Args) > 1 {
		table = os.Args[1]
	}
	raw, err := os.ReadFile(table)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-rules-coverage:", err)
		os.Exit(2)
	}
	cache := map[string]string{}
	var broken []string
	rows, refs := 0, 0
	for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|"), "|")
		if len(cells) != 5 {
			continue // intestazione o separatore con altro numero di colonne
		}
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		id, tests, status, missing := cells[0], cells[2], cells[3], cells[4]
		if id == "Id" || strings.HasPrefix(id, "---") {
			continue
		}
		rows++
		bad := func(format string, a ...any) {
			broken = append(broken, fmt.Sprintf("%s:%d %s: %s", table, i+1, id, fmt.Sprintf(format, a...)))
		}
		if !statuses[status] {
			bad("stato %q non valido", status)
			continue
		}
		found := refRe.FindAllStringSubmatch(tests, -1)
		if (status == "coperta" || status == "parziale") && len(found) == 0 {
			bad("stato %q senza nessun test citato", status)
		}
		if (status == "parziale" || status == "non coperta") && missing == "" {
			bad("stato %q senza «cosa manca»", status)
		}
		for _, m := range found {
			refs++
			if err := check(cache, m[1]); err != nil {
				bad("%v", err)
			}
		}
	}
	if rows == 0 {
		broken = append(broken, table+": nessuna riga di regola trovata")
	}
	if len(broken) > 0 {
		fmt.Fprintf(os.Stderr, "check-rules-coverage: %d problemi\n", len(broken))
		for _, b := range broken {
			fmt.Fprintln(os.Stderr, "  "+b)
		}
		os.Exit(1)
	}
	fmt.Printf("check-rules-coverage: %d regole, %d test citati, tutti presenti\n", rows, refs)
}

func check(cache map[string]string, ref string) error {
	path, name, ok := strings.Cut(ref, "#")
	if !ok || path == "" || name == "" {
		return fmt.Errorf("riferimento %q: atteso `percorso#Nome`", ref)
	}
	src, ok := cache[path]
	if !ok {
		b, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			return fmt.Errorf("file %s: %v", path, err)
		}
		src = strings.ReplaceAll(string(b), "\r\n", "\n")
		cache[path] = src
	}
	if !strings.HasSuffix(path, ".go") {
		if !strings.Contains(src, name) {
			return fmt.Errorf("test %q non trovato in %s", name, path)
		}
		return nil
	}
	parts := strings.Split(name, "/")
	if !strings.Contains(src, "func "+parts[0]+"(") {
		return fmt.Errorf("func %s non trovata in %s", parts[0], path)
	}
	for _, sub := range parts[1:] {
		if !strings.Contains(src, `"`+sub+`"`) {
			return fmt.Errorf("sottotest %q di %s non trovato in %s", sub, parts[0], path)
		}
	}
	return nil
}
