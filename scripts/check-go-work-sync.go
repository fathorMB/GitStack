//go:build ignore

// check-go-work-sync e' l'equivalente Go di scripts/check-go-work-sync.sh
// (GIT-408): lancia `go work sync` dalla radice e fallisce se, dopo, un go.mod,
// un go.sum, go.work o go.work.sum risultano diversi da quelli committati.
//
// La differenza dallo script bash e' il confronto: usa
// `git diff --ignore-cr-at-eol`, perche' su un checkout Windows con
// core.autocrlf=true `git status` segna sporchi go.work e services/git/go.sum
// solo per il CRLF. Non ha bisogno di bash. Si lancia dalla radice del repo:
//
//	go run scripts/check-go-work-sync.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var pathspec = []string{":(glob)**/go.mod", ":(glob)**/go.sum", "go.work", "go.work.sum"}

func run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func main() {
	sync := exec.Command("go", "work", "sync")
	sync.Stdout, sync.Stderr = os.Stdout, os.Stderr
	if err := sync.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "check-go-work-sync: go work sync:", err)
		os.Exit(2)
	}

	// File tracciati: differenze reali, righe che differiscono solo per il CR
	// finale escluse.
	args := append([]string{"git", "--no-pager", "diff", "--ignore-cr-at-eol", "--"}, pathspec...)
	diff, err := run(args...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-go-work-sync: git diff:", err)
		os.Exit(2)
	}
	// File nuovi non tracciati (es. un go.work.sum che prima non c'era).
	args = append([]string{"git", "ls-files", "--others", "--exclude-standard", "--"}, pathspec...)
	untracked, err := run(args...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-go-work-sync: git ls-files:", err)
		os.Exit(2)
	}

	if strings.TrimSpace(diff) != "" || strings.TrimSpace(untracked) != "" {
		fmt.Fprintln(os.Stderr, "\nIl workspace Go non e' sincronizzato con go.work.")
		fmt.Fprintln(os.Stderr, "Lancia 'go work sync' dalla radice e committa i go.mod/go.sum cambiati.")
		if strings.TrimSpace(untracked) != "" {
			fmt.Printf("\n--- file nuovi non tracciati ---\n%s", untracked)
		}
		fmt.Printf("\n--- git diff --ignore-cr-at-eol ---\n%s", diff)
		goenv, _ := run("go", "env", "GOVERSION", "GOTOOLCHAIN", "GOFLAGS", "GOPROXY")
		fmt.Printf("\n--- go env (GOVERSION GOTOOLCHAIN GOFLAGS GOPROXY) ---\n%s", goenv)
		os.Exit(1)
	}
	fmt.Println("Workspace Go sincronizzato.")
}
