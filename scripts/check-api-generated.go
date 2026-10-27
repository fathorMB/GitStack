//go:build ignore

// check-api-generated e' l'equivalente di scripts/check-api-generated.sh
// (GIT-408): rigenera il codice dal contratto (scripts/generate-api.sh, che
// resta bash e richiede Node/pnpm) e fallisce se i file generati risultano
// diversi da quelli committati. Il confronto usa `git diff --ignore-cr-at-eol`
// (su Windows con core.autocrlf=true il CRLF non e' una differenza) piu'
// l'elenco dei file nuovi non tracciati. Si lancia dalla radice del repo:
//
//	go run scripts/check-api-generated.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Stesso elenco di check-api-generated.sh.
var generated = []string{
	"client/go/*.gen.go",
	"client/ts/src/generated",
	"services/gateway/internal/openapi/api.gen.go",
	"services/gateway/internal/identityclient/types.gen.go",
	"services/gateway/internal/security/routes.gen.go",
	"services/core/internal/openapi/api.gen.go",
	"services/identity/internal/openapi/api.gen.go",
	"cli/internal/mcp/tools_gen.go",
}

func run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

// bashPath sceglie la bash di Git for Windows quando c'e': nel PATH di un host
// Windows `bash` puo' essere quella di WSL (C:\Windows\system32\bash.exe), che
// non vede il Go e il Node dell'host ("go: command not found").
func bashPath() string {
	if runtime.GOOS == "windows" {
		// <Git>\mingw64\libexec\git-core -> <Git>\bin\bash.exe (indipendente da
		// eventuali shim di git nel PATH).
		if out, err := run("git", "--exec-path"); err == nil {
			root := filepath.Join(filepath.FromSlash(strings.TrimSpace(out)), "..", "..", "..")
			b := filepath.Join(root, "bin", "bash.exe")
			if _, err := os.Stat(b); err == nil {
				return b
			}
		}
	}
	return "bash"
}

func main() {
	gen := exec.Command(bashPath(), "./scripts/generate-api.sh")
	gen.Stdout, gen.Stderr = os.Stdout, os.Stderr
	if err := gen.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "check-api-generated: generate-api.sh:", err)
		os.Exit(2)
	}

	diff, err := run(append([]string{"git", "--no-pager", "diff", "--ignore-cr-at-eol", "--"}, generated...)...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-api-generated: git diff:", err)
		os.Exit(2)
	}
	untracked, err := run(append([]string{"git", "ls-files", "--others", "--exclude-standard", "--"}, generated...)...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-api-generated: git ls-files:", err)
		os.Exit(2)
	}
	if strings.TrimSpace(diff) != "" || strings.TrimSpace(untracked) != "" {
		fmt.Fprintln(os.Stderr, "\nIl codice generato non e' allineato al contratto OpenAPI.")
		fmt.Fprintln(os.Stderr, "Rilancia ./scripts/generate-api.sh e committa il risultato.")
		if strings.TrimSpace(untracked) != "" {
			fmt.Printf("\n--- file nuovi non tracciati ---\n%s", untracked)
		}
		fmt.Printf("\n--- git diff --ignore-cr-at-eol ---\n%s", diff)
		os.Exit(1)
	}
	fmt.Println("Codice generato allineato al contratto OpenAPI.")
}
