//go:build ignore

// check-internal-versions verifica che ogni modulo del workspace che richiede
// un altro modulo interno (github.com/fathorMB/GitStack/...) lo richieda a una
// pseudo-versione il cui contenuto coincide con quello del workspace (GIT-182).
//
// Perche': i Dockerfile costruiscono ogni servizio fuori dal workspace
// (GOWORK=off), quindi l'immagine prende pkg/* e client/go dalla versione
// scritta nel go.mod, non dalla cartella locale. In CI e nei test il workspace
// maschera il disallineamento.
//
// Per ogni require interno: si ricava l'hash12 dalla pseudo-versione, lo si
// risolve con git rev-parse e si confronta la cartella del modulo a quel
// commit con HEAD (git diff --quiet <hash> HEAD -- <cartella>). Un hash non
// trovato in git e' un errore (serve fetch-depth: 0 in CI). I moduli si
// ricavano da `go work edit -json`. Si lancia dalla radice del repo:
//
//	go run scripts/check-internal-versions.go
//
// Per correggere: nel modulo che fallisce, dopo il commit che cambia il
// pacchetto, `GOWORK=off go get <modulo>@<hash del commit>` e
// `GOWORK=off go mod tidy`, poi `go work sync`.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const internalPrefix = "github.com/fathorMB/GitStack/"

type modInfo struct {
	path string // percorso del modulo
	dir  string // cartella relativa alla radice, con /
}

var pseudoRe = regexp.MustCompile(`-([0-9a-f]{12})$`)

func run(dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
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

func main() {
	out, err := run(".", "go", "work", "edit", "-json")
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
		Require []struct {
			Path     string
			Version  string
			Indirect bool
		}
	}
	mods := map[string]modInfo{}
	parsed := map[string]goMod{}
	var order []string
	for _, u := range work.Use {
		dir := filepath.ToSlash(filepath.Clean(u.DiskPath))
		o, err := run(".", "go", "mod", "edit", "-json", filepath.Join(dir, "go.mod"))
		if err != nil {
			fatal(err)
		}
		var m goMod
		if err := json.Unmarshal(o, &m); err != nil {
			fatal(err)
		}
		mods[m.Module.Path] = modInfo{path: m.Module.Path, dir: dir}
		parsed[m.Module.Path] = m
		order = append(order, m.Module.Path)
	}

	failed := 0
	checked := 0
	for _, mp := range order {
		m := parsed[mp]
		from := mods[mp]
		for _, r := range m.Require {
			if !strings.HasPrefix(r.Path, internalPrefix) {
				continue
			}
			dep, ok := mods[r.Path]
			if !ok {
				continue // modulo interno non nel workspace
			}
			checked++
			sm := pseudoRe.FindStringSubmatch(r.Version)
			if sm == nil {
				fmt.Printf("FAIL %s: require %s %s non e' una pseudo-versione\n", from.dir, r.Path, r.Version)
				failed++
				continue
			}
			// Niente "^{commit}": con un git avviato tramite cmd.exe il ^ viene mangiato.
			full, err := run(".", "git", "rev-parse", "--verify", "--quiet", sm[1])
			if err != nil {
				fmt.Printf("FAIL %s: require %s %s: commit %s non trovato in git (in CI serve fetch-depth: 0)\n",
					from.dir, r.Path, r.Version, sm[1])
				failed++
				continue
			}
			hash := strings.TrimSpace(string(full))
			cmd := exec.Command("git", "diff", "--quiet", hash, "HEAD", "--", dep.dir)
			if err := cmd.Run(); err != nil {
				latest, _ := run(".", "git", "log", "-1", "--format=%h", "HEAD", "--", dep.dir)
				fmt.Printf("FAIL %s: require %s %s (commit %s) ha contenuto diverso da %s/ del workspace.\n"+
					"     Atteso il commit %s. Correggi: cd %s && GOWORK=off go get %s@%s && GOWORK=off go mod tidy\n",
					from.dir, r.Path, r.Version, sm[1], dep.dir,
					strings.TrimSpace(string(latest)), from.dir, r.Path, strings.TrimSpace(string(latest)))
				failed++
				continue
			}
			fmt.Printf("ok   %s -> %s %s\n", from.dir, dep.dir, sm[1])
		}
	}
	if failed > 0 {
		fmt.Printf("check-internal-versions: %d require interni non allineati al workspace\n", failed)
		os.Exit(1)
	}
	fmt.Printf("check-internal-versions: %d require interni allineati\n", checked)
}
