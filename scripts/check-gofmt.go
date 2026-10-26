//go:build ignore

// check-gofmt fallisce se un file .go tracciato da git non è formattato come
// gofmt (golangci-lint dei check go-lint-* non lo controlla). Guarda il
// contenuto committato (blob di HEAD, in LF), non il worktree:
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
	out, err := git("ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-gofmt: git ls-tree:", err)
		os.Exit(2)
	}
	var input strings.Builder
	var names, objectIDs []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		metadata, name, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasSuffix(name, ".go") {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 {
			fmt.Fprintln(os.Stderr, "check-gofmt: formato inatteso da git ls-tree:", entry)
			os.Exit(2)
		}
		input.WriteString(fields[2])
		input.WriteByte('\n')
		names = append(names, name)
		objectIDs = append(objectIDs, fields[2])
	}

	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	batch, err := cmd.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-gofmt: git cat-file --batch:", err)
		os.Exit(2)
	}

	var bad []string
	pos := 0
	for i, name := range names {
		lineEnd := bytes.IndexByte(batch[pos:], '\n')
		if lineEnd < 0 {
			fmt.Fprintln(os.Stderr, "check-gofmt: intestazione cat-file mancante per", name)
			os.Exit(2)
		}
		header := strings.Fields(string(batch[pos : pos+lineEnd]))
		if len(header) != 3 || header[0] != objectIDs[i] || header[1] != "blob" {
			fmt.Fprintln(os.Stderr, "check-gofmt: risposta cat-file non valida per", name)
			os.Exit(2)
		}
		var size int
		if _, err := fmt.Sscanf(header[2], "%d", &size); err != nil || size < 0 {
			fmt.Fprintln(os.Stderr, "check-gofmt: dimensione blob non valida per", name)
			os.Exit(2)
		}
		pos += lineEnd + 1
		if size > len(batch)-pos {
			fmt.Fprintln(os.Stderr, "check-gofmt: blob troncato per", name)
			os.Exit(2)
		}
		src := batch[pos : pos+size]
		pos += size
		if pos >= len(batch) || batch[pos] != '\n' {
			fmt.Fprintln(os.Stderr, "check-gofmt: terminatore blob mancante per", name)
			os.Exit(2)
		}
		pos++
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
	fmt.Printf("check-gofmt: %d file .go formattati\n", len(names))
}
