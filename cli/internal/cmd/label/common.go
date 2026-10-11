package label

import (
	"context"
	"fmt"
	"strings"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// env è quello che un sottocomando usa: client generato, istanza, repo.
type env struct {
	f     *cmdutil.Factory
	gen   *gitstack.ClientWithResponses
	host  string
	owner string
	repo  string
}

// newEnv risolve repo (-R o origin), istanza e token (G7) e costruisce il
// client. Il repo si risolve prima, così un uso errato esce 2 senza rete.
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
	return &env{f: f, gen: cl.Generated(), host: host, owner: owner, repo: repo}, nil
}

// check converte una risposta non-2xx nell'errore di gs.
func (e *env) check(status int, body []byte) error { return api.CheckStatus(status, body) }

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// webBase è l'indirizzo web dell'istanza, senza /api/v1.
func (e *env) webBase() string { return strings.TrimSuffix(api.BaseURL(e.host), api.BasePath) }

// webURL è la pagina web dell'etichetta nel repo.
func (e *env) webURL(name string) string {
	return fmt.Sprintf("%s/%s/%s/-/labels/%s", e.webBase(), e.owner, e.repo, name)
}

// listLabels pagella le etichette fino al limite richiesto.
func (e *env) listLabels(ctx context.Context, limit int) ([]gitstack.Label, error) {
	var items []gitstack.Label
	per := 100
	for page := 1; len(items) < limit; page++ {
		p := page
		resp, err := e.gen.ListLabelsWithResponse(ctx, gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), &gitstack.ListLabelsParams{Page: &p, PerPage: &per})
		if err != nil {
			return nil, err
		}
		if err := e.check(resp.StatusCode(), resp.Body); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, unexpected(resp.StatusCode())
		}
		items = append(items, resp.JSON200.Items...)
		if len(resp.JSON200.Items) == 0 || page*per >= resp.JSON200.Total {
			break
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
