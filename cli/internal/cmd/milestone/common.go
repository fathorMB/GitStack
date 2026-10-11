package milestone

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/spf13/cobra"

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

// parseNumber legge il numero di una milestone: `12`, `#12` o l'indirizzo web.
func parseNumber(arg string) (int64, error) {
	s := strings.TrimSpace(arg)
	if i := strings.LastIndex(s, "/milestones/"); i >= 0 {
		s = s[i+len("/milestones/"):]
		s = strings.TrimRight(strings.SplitN(strings.SplitN(s, "#", 2)[0], "?", 2)[0], "/")
	}
	s = strings.TrimPrefix(s, "#")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, cmdutil.UsageErrorf("numero di milestone non valido: %q (atteso `12` o `#12`)", arg)
	}
	return n, nil
}

// numberArg è il validatore degli argomenti dei comandi `<numero>`.
func numberArg(c *cobra.Command, args []string) (int64, error) {
	if len(args) != 1 {
		return 0, cmdutil.UsageErrorf("serve il numero della milestone: %s <numero>", c.CommandPath())
	}
	return parseNumber(args[0])
}

// check converte una risposta non-2xx nell'errore di gs.
func (e *env) check(status int, body []byte) error { return api.CheckStatus(status, body) }

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// webBase è l'indirizzo web dell'istanza, senza /api/v1.
func (e *env) webBase() string { return strings.TrimSuffix(api.BaseURL(e.host), api.BasePath) }

// webURL è la pagina web della milestone nel repo.
func (e *env) webURL(n int64) string {
	return fmt.Sprintf("%s/%s/%s/milestones/%d", e.webBase(), e.owner, e.repo, n)
}

// listMilestones pagella le milestone fino al limite richiesto.
func (e *env) listMilestones(ctx context.Context, limit int, state *gitstack.ListMilestonesParamsState) ([]gitstack.Milestone, error) {
	var items []gitstack.Milestone
	per := 100
	for page := 1; len(items) < limit; page++ {
		p := page
		params := &gitstack.ListMilestonesParams{Page: &p, PerPage: &per}
		if state != nil {
			params.State = state
		}
		resp, err := e.gen.ListMilestonesWithResponse(ctx, gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), params)
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
