//go:build ignore

// check-gofmt fallisce se un file .go tracciato da git non è formattato come
// gofmt (golangci-lint dei check go-lint-* non lo controlla). Guarda il
// contenuto committato (git show HEAD:<file>, blob in LF), non il worktree:
// su un checkout Windows con core.autocrlf=true i file sono CRLF e gofmt li
// segnalerebbe tutti. Si lancia dalla radice del repo:
//
//	go run scripts/check-gofmt.go
//
// Per correggere: git show HEAD:<file> | gofmt > <file>, oppure gofmt -w su un
// checkout LF.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"strings"
)

func git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func main() {
	out, err := git("ls-tree", "-r", "--name-only", "-z", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-gofmt: git ls-tree:", err)
		os.Exit(2)
	}
	var bad []string
	n := 0
	for _, name := range strings.Split(string(out), "\x00") {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		n++
		src, err := git("show", "HEAD:"+name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "check-gofmt: git show", name+":", err)
			os.Exit(2)
		}
		res, err := format.Source(src)
		if err != nil {
			fmt.Printf("%s: %v\n", name, err)
			bad = append(bad, name)
			continue
		}
		if !bytes.Equal(src, res) {
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		fmt.Println("File .go non formattati con gofmt (sul contenuto committato):")
		for _, b := range bad {
			fmt.Println("  " + b)
		}
		os.Exit(1)
	}
	fmt.Printf("check-gofmt: %d file .go formattati\n", n)
}
