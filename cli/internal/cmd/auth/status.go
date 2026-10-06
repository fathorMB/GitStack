package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// Stati di un'istanza in `gs auth status`.
const (
	statusOK      = "ok"
	statusInvalid = "invalid"  // token scaduto, revocato o rifiutato (exit 4)
	statusNoToken = "no_token" // istanza configurata senza token (exit 4)
	statusError   = "error"    // istanza non raggiungibile o risposta inattesa (exit 1)
)

// StatusFields sono i campi di --json per `gs auth status`.
var StatusFields = []string{"host", "user", "source", "scopes", "expiresAt", "status", "error"}

// hostStatus è lo stato di una istanza; non contiene mai il token.
type hostStatus struct {
	Host      string     `json:"host"`
	User      string     `json:"user"`
	Source    string     `json:"source"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Status    string     `json:"status"`
	Error     string     `json:"error"`
}

func newStatusCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Mostra le istanze configurate con utente, scope e scadenza del token",
		Long: "Per ogni istanza configurata (o solo per --hostname) verifica il token con l'API e mostra\n" +
			"utente, scope e scadenza. Esce con 4 se un token è scaduto, revocato o mancante.\n" +
			"GS_TOKEN vale solo per l'istanza di GS_HOST (o --hostname): non si manda mai a un'altra.",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runStatus(c.Context(), f, &jo)
		},
	}
	output.AddJSONFlags(cmd, &jo, StatusFields)
	return cmd
}

func runStatus(ctx context.Context, f *cmdutil.Factory, jo *output.JSONOptions) error {
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	stored, err := f.StoredTokens()
	if err != nil {
		return err
	}
	envHost := normalizeHost(f.HostnameFlag)
	if envHost == "" {
		envHost = normalizeHost(f.Getenv(cmdutil.EnvHost))
	}
	envToken := f.Getenv(config.EnvToken)
	if envHost == "" && envToken != "" {
		envHost, _ = cfg.DefaultHost()
	}

	hosts, err := cfg.Hosts()
	if err != nil {
		return err
	}
	if flag := normalizeHost(f.HostnameFlag); flag != "" {
		hosts = []string{flag}
	} else if envHost != "" && !contains(hosts, envHost) {
		hosts = append(hosts, envHost)
	}
	if len(hosts) == 0 {
		return cmdutil.NotAuthenticatedError("nessuna istanza configurata: esegui `gs auth login` o imposta %s e %s", cmdutil.EnvHost, config.EnvToken)
	}

	var res []hostStatus
	worst := 0
	for _, h := range hosts {
		token, source := "", ""
		if envToken != "" && h == envHost {
			token, source = envToken, config.SourceEnv
		} else if t, src, err := stored.Token(h); err == nil {
			token, source = t, src
		}
		st := checkHost(ctx, f, h, token, source)
		if code := statusExit(st.Status); code > worst {
			worst = code
		}
		res = append(res, st)
	}

	if jo.Enabled() {
		if err := jo.Write(f.IO.Out, res, f.IO.OutTTY); err != nil {
			return err
		}
	} else {
		t := output.NewTable(f.IO.OutTTY, f.IO.Width, "ISTANZA", "UTENTE", "TOKEN", "SCOPE", "SCADENZA", "STATO")
		for _, s := range res {
			t.AddRow(s.Host, dash(s.User), dash(s.Source), dash(strings.Join(s.Scopes, ",")), expiryLabel(s.ExpiresAt, s.Status), statusLabel(s))
		}
		if err := t.Render(f.IO.Out); err != nil {
			return err
		}
	}
	switch worst {
	case cmdutil.ExitUnauthorized:
		return cmdutil.NotAuthenticatedError("token scaduto, revocato o mancante: esegui `gs auth login` per le istanze segnalate")
	case cmdutil.ExitGeneric:
		return errors.New("almeno un'istanza non è raggiungibile")
	}
	return nil
}

func statusExit(s string) int {
	switch s {
	case statusInvalid, statusNoToken:
		return cmdutil.ExitUnauthorized
	case statusError:
		return cmdutil.ExitGeneric
	}
	return cmdutil.ExitOK
}

func checkHost(ctx context.Context, f *cmdutil.Factory, host, token, source string) hostStatus {
	st := hostStatus{Host: host, Source: source, Scopes: []string{}}
	if token == "" {
		st.Status = statusNoToken
		st.Error = "nessun token: esegui `gs auth login`"
		return st
	}
	cl, err := api.New(api.Options{Host: host, Token: token, Version: f.Version, HTTP: f.HTTPClient()})
	if err != nil {
		st.Status, st.Error = statusError, scrub(err, token).Error()
		return st
	}
	resp, err := cl.Generated().GetCurrentSessionWithResponse(ctx)
	if err != nil {
		st.Status, st.Error = statusError, scrub(err, token).Error()
		return st
	}
	if err := api.CheckStatus(resp.StatusCode(), resp.Body); err != nil {
		var ae *cmdutil.APIError
		if errors.As(err, &ae) && ae.Status == 401 {
			st.Status, st.Error = statusInvalid, "token scaduto o revocato"
		} else {
			st.Status, st.Error = statusError, err.Error()
		}
		return st
	}
	if resp.JSON200 == nil {
		st.Status, st.Error = statusError, fmt.Sprintf("risposta inattesa (HTTP %d)", resp.StatusCode())
		return st
	}
	s := resp.JSON200
	st.Status = statusOK
	st.User = string(s.User.Username)
	st.ExpiresAt = s.ExpiresAt
	if s.Scopes != nil {
		for _, sc := range *s.Scopes {
			st.Scopes = append(st.Scopes, string(sc))
		}
	}
	return st
}

func statusLabel(s hostStatus) string {
	if s.Error != "" {
		return s.Status + ": " + s.Error
	}
	return s.Status
}

func expiryLabel(t *time.Time, status string) string {
	if status != statusOK {
		return "-"
	}
	if t == nil {
		return "mai"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
