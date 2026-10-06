package repo

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// env è quello che un sottocomando usa: client generato e istanza.
type env struct {
	f    *cmdutil.Factory
	gen  *gitstack.ClientWithResponses
	host string
}

// newEnv risolve istanza e token (G7) e costruisce il client.
func newEnv(f *cmdutil.Factory) (*env, error) {
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
	return &env{f: f, gen: cl.Generated(), host: host}, nil
}

// target risolve il repo di un comando: l'argomento `owner/repo` (o l'URL di
// clone) se c'è, altrimenti -R e infine il remote origin (G7). Si fa prima
// della rete, così un uso errato esce 2 senza chiamate.
func target(f *cmdutil.Factory, args []string) (owner, repo string, err error) {
	if len(args) == 0 {
		return f.BaseRepo()
	}
	return parseTarget(args[0])
}

func parseTarget(arg string) (owner, repo string, err error) {
	s := strings.TrimSpace(arg)
	if strings.Contains(s, "://") || strings.Contains(s, "@") {
		r, err := gsrepo.ParseRemote(s)
		if err != nil {
			return "", "", cmdutil.UsageErrorf("repo non valido: %q (atteso `owner/repo`)", arg)
		}
		return r.Owner, r.Repo, nil
	}
	o, r, err := gsrepo.ParseRepoFlag(s)
	if err != nil {
		return "", "", cmdutil.UsageErrorf("repo non valido: %q (atteso `owner/repo`)", arg)
	}
	return o, strings.TrimSuffix(r, ".git"), nil
}

// check converte una risposta non-2xx nell'errore di gs.
func (e *env) check(status int, body []byte) error { return api.CheckStatus(status, body) }

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// webBase è l'indirizzo web dell'istanza, senza /api/v1.
func (e *env) webBase() string { return strings.TrimSuffix(api.BaseURL(e.host), api.BasePath) }

// webURL è la pagina web del repo.
func (e *env) webURL(owner, name string) string {
	return fmt.Sprintf("%s/%s/%s", e.webBase(), owner, name)
}

// get legge un repo.
func (e *env) get(ctx context.Context, owner, name string) (*gitstack.Repository, error) {
	resp, err := e.gen.GetRepositoryWithResponse(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	return resp.JSON200, nil
}

// me è il nome utente di chi sta chiamando.
func (e *env) me(ctx context.Context) (string, error) {
	resp, err := e.gen.GetCurrentSessionWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", unexpected(resp.StatusCode())
	}
	return string(resp.JSON200.User.Username), nil
}

// openURL apre un indirizzo nel browser; nei test si sostituisce.
var openURL = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }
