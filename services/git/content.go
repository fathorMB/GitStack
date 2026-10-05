package main

import (
	"fmt"
	"strings"

	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
)

// content è il collegamento provvisorio ai modelli di contenuto iniziale:
// il README è già generato qui, i modelli .gitignore e licenza arrivano dal
// pacchetto services/git/internal/templates (GIT-69). Finché non c'è, ogni id
// di modello è "sconosciuto" (400).
type content struct{}

func newContent() httpserver.Content { return content{} }

func (content) Gitignore(string) ([]byte, error) { return nil, httpserver.ErrUnknownTemplate }

func (content) License(string, int, string) ([]byte, error) {
	return nil, httpserver.ErrUnknownTemplate
}

func (content) Readme(name, description string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", name)
	if strings.TrimSpace(description) != "" {
		fmt.Fprintf(&b, "\n%s\n", description)
	}
	return []byte(b.String())
}
