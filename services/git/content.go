package main

import (
	"errors"

	"github.com/fathorMB/GitStack/services/git/internal/httpserver"
	"github.com/fathorMB/GitStack/services/git/internal/templates"
)

// content collega l'API interna ai modelli di contenuto iniziale (R5) del
// pacchetto templates, mappando il suo errore su quello dell'handler (400).
type content struct{}

func newContent() httpserver.Content { return content{} }

func mapErr(err error) error {
	if errors.Is(err, templates.ErrUnknownTemplate) {
		return httpserver.ErrUnknownTemplate
	}
	return err
}

func (content) Gitignore(id string) ([]byte, error) {
	b, err := templates.Gitignore(id)
	return b, mapErr(err)
}

func (content) License(id string, year int, holder string) ([]byte, error) {
	b, err := templates.License(id, year, holder)
	return b, mapErr(err)
}

func (content) Readme(name, description string) []byte { return templates.Readme(name, description) }
