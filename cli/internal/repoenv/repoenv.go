// Package repoenv è quello che i comandi di lettura del codice (search,
// blame, browse) condividono: risoluzione di istanza, token e repo (G7) e
// client generato.
package repoenv

import (
	"fmt"
	"net/url"
	"strings"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// Env è l'ambiente di un comando su un repo.
type Env struct {
	F     *cmdutil.Factory
	Gen   *gitstack.ClientWithResponses
	Host  string
	Owner string
	Repo  string
}

// Client costruisce il client per host e token (senza repo).
func Client(f *cmdutil.Factory) (*gitstack.ClientWithResponses, string, error) {
	host, err := f.Host()
	if err != nil {
		return nil, "", err
	}
	tok, err := f.Token(host)
	if err != nil {
		return nil, "", err
	}
	cl, err := api.New(api.Options{Host: host, Token: tok, Version: f.Version, HTTP: f.HTTPClient()})
	if err != nil {
		return nil, "", err
	}
	return cl.Generated(), host, nil
}

// New risolve il repo (-R o origin) prima della rete, poi il client.
func New(f *cmdutil.Factory) (*Env, error) {
	owner, repo, err := f.BaseRepo()
	if err != nil {
		return nil, err
	}
	gen, host, err := Client(f)
	if err != nil {
		return nil, err
	}
	return &Env{F: f, Gen: gen, Host: host, Owner: owner, Repo: repo}, nil
}

// Check converte una risposta non-2xx nell'errore di gs.
func Check(status int, body []byte) error { return api.CheckStatus(status, body) }

// Unexpected è l'errore di una risposta 2xx senza il corpo atteso.
func Unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// WebBase è l'indirizzo della UI dell'istanza, senza `/` finale.
func WebBase(host string) string {
	return strings.TrimSuffix(api.BaseURL(host), api.BasePath)
}

// EscapePath codifica ogni segmento di un percorso (come encodePath della UI).
func EscapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
