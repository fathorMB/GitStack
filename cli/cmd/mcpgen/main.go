// Command mcpgen genera cli/internal/mcp/tools_gen.go dal contratto OpenAPI:
// gli strumenti di `gs mcp` sono le operazioni con x-mcp.expose=true.
//
// Uso, da cli/internal/mcp (go generate):
//
//	go run ../../cmd/mcpgen -spec ../../../api/openapi.yaml -out tools_gen.go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/fathorMB/GitStack/cli/internal/mcp/toolgen"
)

func main() {
	spec := flag.String("spec", "../../../api/openapi.yaml", "contratto OpenAPI")
	out := flag.String("out", "tools_gen.go", "file Go da scrivere")
	pkg := flag.String("pkg", "mcp", "nome del package")
	flag.Parse()
	if err := run(*spec, *out, *pkg); err != nil {
		fmt.Fprintf(os.Stderr, "mcpgen: %v\n", err)
		os.Exit(1)
	}
}

func run(specPath, outPath, pkg string) error {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	src, err := toolgen.Generate(spec, pkg)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, src, 0o644)
}
