// Command gsdocs genera la reference dei comandi di gs in Markdown dall'albero
// cobra reale (github.com/spf13/cobra/doc, GenMarkdownTree): niente si scrive
// a mano. Lo lancia docs-site/scripts/generate.mjs; l'output non si committa.
//
// Uso, da cli/:
//
//	go run ./cmd/gsdocs <cartella-di-output>
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra/doc"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "uso: gsdocs <cartella-di-output>")
		os.Exit(2)
	}
	if err := generate(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "gsdocs: %v\n", err)
		os.Exit(1)
	}
}

// generate scrive una pagina per ogni comando di gs in dir. Le pagine hanno
// un front matter con titolo e id stabili, e nessuna data: due giri sullo
// stesso commit producono gli stessi byte.
func generate(dir string) error {
	cmd := root.NewCmd(cmdutil.New("dev", cmdutil.System()))
	cmd.DisableAutoGenTag = true
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	prepender := func(name string) string {
		base := strings.TrimSuffix(filepath.Base(name), ".md")
		title := strings.ReplaceAll(base, "_", " ")
		return fmt.Sprintf("---\nid: %s\ntitle: %q\nsidebar_label: %q\n---\n\n", base, title, title)
	}
	link := func(name string) string { return name }
	if err := doc.GenMarkdownTreeCustom(cmd, dir, prepender, link); err != nil {
		return err
	}
	return nil
}
