package app

import (
	"fmt"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// env è quello che un sottocomando usa: client generato, istanza, repo.
type env struct {
	f     *cmdutil.Factory
	gen   *gitstack.ClientWithResponses
	owner string
	repo  string
}

// newEnv risolve repo (-R o origin), istanza e token (G7) e costruisce il client. Il repo si
// risolve prima, così un uso errato esce 2 senza rete.
func newEnv(f *cmdutil.Factory) (*env, error) {
	owner, repo, err := f.BaseRepo()
	if err != nil {
		return nil, err
	}
	host, err := f.Host()
	if err != nil {
		return nil, err
	}
	tok, err := f.Token(host)
	if err != nil {
		return nil, err
	}
	cl, err := api.New(api.Options{Host: host, Token: tok, Version: f.Version, HTTP: f.HTTPClient()})
	if err != nil {
		return nil, err
	}
	return &env{f: f, gen: cl.Generated(), owner: owner, repo: repo}, nil
}

// check converte una risposta non-2xx nell'errore di gs.
func (e *env) check(status int, body []byte) error { return api.CheckStatus(status, body) }

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}
