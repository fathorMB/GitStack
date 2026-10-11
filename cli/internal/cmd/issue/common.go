package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// env è quello che un sottocomando usa: client generato, istanza e repo.
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

// check converte una risposta non-2xx nell'errore di gs. Un repo archiviato
// (409 `archived`, R10) ha un messaggio che dice cosa fare.
func (e *env) check(status int, body []byte) error {
	err := api.CheckStatus(status, body)
	var ae *cmdutil.APIError
	if errors.As(err, &ae) && ae.Code == "archived" {
		ae.Message = fmt.Sprintf("il repository %s/%s è archiviato (sola lettura): issue e commenti non si possono modificare finché non viene riattivato", e.owner, e.repo)
	}
	return err
}

// webURL è l'indirizzo della pagina web della issue.
func (e *env) webURL(n int64) string {
	base := strings.TrimSuffix(api.BaseURL(e.host), api.BasePath)
	return fmt.Sprintf("%s/%s/%s/issues/%d", base, e.owner, e.repo, n)
}

// me è il nome utente di chi sta chiamando (per `@me`).
func (e *env) me(ctx context.Context) (string, error) {
	resp, err := e.gen.GetCurrentSessionWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", resp.StatusCode())
	}
	return string(resp.JSON200.User.Username), nil
}

// users sostituisce `@me` con il nome di chi chiama; toglie vuoti e doppioni.
func (e *env) users(ctx context.Context, names []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	var me string
	for _, n := range names {
		n = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(n), "@"))
		if n == "" {
			continue
		}
		if n == "me" {
			if me == "" {
				m, err := e.me(ctx)
				if err != nil {
					return nil, err
				}
				me = m
			}
			n = me
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// milestoneNumber risolve una milestone, indicata con il numero o con il
// titolo (senza distinguere maiuscole), nel suo numero.
func (e *env) milestoneNumber(ctx context.Context, s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return n, nil
	}
	if s == "" {
		return 0, cmdutil.UsageErrorf("milestone vuota")
	}
	state := gitstack.ListMilestonesParamsStateAll
	per := 100
	for page := 1; ; page++ {
		resp, err := e.gen.ListMilestonesWithResponse(ctx, e.owner, e.repo, &gitstack.ListMilestonesParams{State: &state, Page: &page, PerPage: &per})
		if err != nil {
			return 0, err
		}
		if err := e.check(resp.StatusCode(), resp.Body); err != nil {
			return 0, err
		}
		if resp.JSON200 == nil {
			return 0, fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", resp.StatusCode())
		}
		for _, m := range resp.JSON200.Items {
			if strings.EqualFold(m.Title, s) {
				return m.Number, nil
			}
		}
		if len(resp.JSON200.Items) == 0 || page*per >= resp.JSON200.Total {
			break
		}
	}
	return 0, &cmdutil.ExitError{Code: cmdutil.ExitNotFound, ErrCode: "not_found", Err: fmt.Errorf("milestone %q non trovata in %s/%s", s, e.owner, e.repo)}
}

// parseNumber legge il numero di una issue: `12`, `#12` o l'indirizzo web.
func parseNumber(arg string) (int64, error) {
	s := strings.TrimSpace(arg)
	if i := strings.LastIndex(s, "/issues/"); i >= 0 {
		s = s[i+len("/issues/"):]
		s = strings.TrimRight(strings.SplitN(strings.SplitN(s, "#", 2)[0], "?", 2)[0], "/")
	}
	s = strings.TrimPrefix(s, "#")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, cmdutil.UsageErrorf("numero di issue non valido: %q (atteso `12` o `#12`)", arg)
	}
	return n, nil
}

// numberArg è il validatore degli argomenti dei comandi `<numero>`.
func numberArg(c *cobra.Command, args []string) (int64, error) {
	if len(args) != 1 {
		return 0, cmdutil.UsageErrorf("serve il numero della issue: %s <numero>", c.CommandPath())
	}
	return parseNumber(args[0])
}

// readBody dà il testo di -b/--body o -F/--body-file (`-` = stdin). I due
// insieme sono un uso errato. set dice se uno dei due era presente.
func readBody(f *cmdutil.Factory, body string, bodySet bool, file string) (text string, set bool, err error) {
	if bodySet && file != "" {
		return "", false, cmdutil.UsageErrorf("--body e --body-file sono alternativi")
	}
	if file == "" {
		return body, bodySet, nil
	}
	var b []byte
	if file == "-" {
		b, err = io.ReadAll(f.IO.In)
	} else {
		b, err = os.ReadFile(file)
	}
	if err != nil {
		return "", false, fmt.Errorf("lettura del testo da %s: %w", file, err)
	}
	return string(b), true, nil
}

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

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
