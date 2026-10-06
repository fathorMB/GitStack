// Package notification è il gruppo `gs notification` (C4): list, read e view
// sulla casella /notifications (M-06), la stessa per persone e agenti.
//
// Output JSON (--json, --jq): i campi sono quelli dello schema `Notification`
// dell'API, più `url` (pagina web della issue collegata, vuoto se la notifica
// non ha una issue). Sono elencati in Fields e documentati in cli/README.md.
package notification

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// Fields sono i campi di --json di list e view.
var Fields = []string{
	"id", "reason", "read", "archived", "event", "summary", "repository", "issue",
	"commentId", "webhook", "actor", "createdAt", "readAt", "url",
}

// notificationOut è una notifica con l'indirizzo web della issue collegata.
type notificationOut struct {
	gitstack.Notification
	URL string `json:"url"`
}

// Reasons sono i motivi ammessi da --reason (NotificationReason).
var Reasons = []string{"assigned", "mentioned", "participating", "subscribed", "commit_linked", "state_change", "webhook"}

// NewCmd restituisce il comando padre `gs notification`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notification",
		Short: "Leggi e gestisci le notifiche",
		Long: "Legge la casella delle notifiche dell'utente corrente, persona o agente (C4). Il filtro per repo è " +
			"il flag globale -R/--repo; senza, le notifiche sono di tutti i repo.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newListCmd(f), newReadCmd(f), newViewCmd(f))
	return cmd
}

// env è quello che un sottocomando usa: client generato e istanza.
type env struct {
	f    *cmdutil.Factory
	gen  *gitstack.ClientWithResponses
	host string
}

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

// webURL è l'indirizzo della issue collegata, vuoto senza repo o issue.
func (e *env) webURL(n gitstack.Notification) string {
	if n.Repository == nil || n.Issue == nil {
		return ""
	}
	base := strings.TrimSuffix(api.BaseURL(e.host), api.BasePath)
	return fmt.Sprintf("%s/%s/issues/%d", base, n.Repository.FullName, n.Issue.Number)
}

func (e *env) out(n gitstack.Notification) notificationOut {
	return notificationOut{Notification: n, URL: e.webURL(n)}
}

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// repoFilter è il filtro -R/--repo, se dato (nessun ripiego sul remote
// origin: la casella è dell'utente, non del repo corrente).
func repoFilter(f *cmdutil.Factory) (*string, error) {
	r := strings.TrimSpace(f.RepoFlag)
	if r == "" {
		return nil, nil
	}
	if parts := strings.Split(r, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, cmdutil.UsageErrorf("--repo: atteso owner/repo, non %q", r)
	}
	return &r, nil
}

// reasonFilter valida e unisce i motivi di --reason.
func reasonFilter(reasons []string) (*string, error) {
	var out []string
	for _, r := range reasons {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		ok := false
		for _, a := range Reasons {
			if a == r {
				ok = true
			}
		}
		if !ok {
			return nil, cmdutil.UsageErrorf("--reason sconosciuto %q; ammessi: %s", r, strings.Join(Reasons, ", "))
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, nil
	}
	s := strings.Join(out, ",")
	return &s, nil
}

// parseID legge l'id di una notifica (UUID).
func parseID(arg string) (gitstack.NotificationIdParam, error) {
	var id gitstack.NotificationIdParam
	if err := id.UnmarshalText([]byte(strings.TrimSpace(arg))); err != nil {
		return id, cmdutil.UsageErrorf("id di notifica non valido: %q (atteso un UUID, come in `gs notification list`)", arg)
	}
	return id, nil
}

