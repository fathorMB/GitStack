// Command specdump scrive il contratto OpenAPI incorporato nel modulo api
// nel file indicato come unico argomento. Serve ai go:generate degli altri
// moduli: `go run github.com/fathorMB/GitStack/api/cmd/specdump <file>`.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/fathorMB/GitStack/api"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "uso: specdump <file di destinazione>")
		return 2
	}
	if err := os.WriteFile(args[0], api.Spec, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "specdump: scrittura di %s: %v\n", args[0], err)
		return 1
	}
	return 0
}
