// Command promptgen genera cli/internal/mcp/prompts_gen.go dalle skills di
// skills/: i prompt di `gs mcp` sono i flussi delle skills (M4).
//
// Uso, da cli/internal/mcp (go generate):
//
//	go run ../../cmd/promptgen -skills ../../../skills -out prompts_gen.go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fathorMB/GitStack/cli/internal/mcp/promptgen"
)

func main() {
	skills := flag.String("skills", "../../../skills", "cartella delle skills")
	out := flag.String("out", "prompts_gen.go", "file Go da scrivere")
	pkg := flag.String("pkg", "mcp", "nome del package")
	flag.Parse()
	src, err := promptgen.Generate(*skills, *pkg)
	if err == nil {
		err = os.WriteFile(*out, src, 0o644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptgen: %v\n", err)
		os.Exit(1)
	}
}
