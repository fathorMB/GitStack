package auth

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
)

// maxTokenInput limita quanto si legge da stdin con --with-token.
const maxTokenInput = 1 << 16

func newLoginCmd(f *cmdutil.Factory) *cobra.Command {
	var withToken, web bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Accedi a un'istanza di GitStack con un token personale",
		Long: "Chiede l'istanza e un token personale (input nascosto), verifica il token con l'API e lo salva:\n" +
			"nel portachiavi di sistema se c'è, altrimenti nel file di configurazione solo-utente.\n\n" +
			"  gs auth login                          interattivo\n" +
			"  gs auth login --hostname H --web       apre la pagina «nuovo token» della UI con gli scope consigliati\n" +
			"  echo $TOKEN | gs auth login --hostname H --with-token\n\n" +
			"Per agenti e CI il login non serve: imposta GS_HOST e GS_TOKEN.",
		Args: cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runLogin(c.Context(), f, withToken, web)
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "Leggi il token da stdin")
	cmd.Flags().BoolVar(&web, "web", false, "Apri nel browser la pagina per creare un token con gli scope consigliati")
	return cmd
}

func runLogin(ctx context.Context, f *cmdutil.Factory, withToken, web bool) error {
	ios := f.IO
	host, err := loginHost(f)
	if err != nil {
		return err
	}

	var token string
	switch {
	case withToken:
		token, err = readAll(ios.In)
	default:
		if !ios.InTTY {
			return cmdutil.UsageErrorf("niente terminale: passa il token su stdin con --with-token")
		}
		if web {
			u := newTokenURL(host)
			_, _ = fmt.Fprintf(ios.ErrOut, "Apro %s\nCrea il token nel browser, poi incollalo qui.\n", u)
			if err := f.OpenBrowser(u); err != nil {
				_, _ = fmt.Fprintf(ios.ErrOut, "Non riesco ad aprire il browser (%v): apri l'indirizzo a mano.\n", err)
			}
		}
		token, err = promptSecret(ios, "Token: ")
	}
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return cmdutil.UsageErrorf("token vuoto")
	}

	user, err := verifyToken(ctx, f, host, token)
	if err != nil {
		return err
	}

	ts, err := f.Tokens()
	if err != nil {
		return err
	}
	if err := ts.SetToken(host, token); err != nil {
		return fmt.Errorf("salvataggio del token: %w", err)
	}
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	hc, _, err := cfg.Host(host)
	if err != nil {
		return err
	}
	hc.User = user
	if hc.GitProtocol == "" {
		hc.GitProtocol = "https"
	}
	if err := cfg.SaveHost(host, hc); err != nil {
		return err
	}
	where := ""
	if _, src, err := ts.Token(host); err == nil && (src == config.SourceKeyring || src == config.SourceFile) {
		where = fmt.Sprintf(" (token nel %s)", sourceLabel(src))
	}
	_, _ = fmt.Fprintf(ios.ErrOut, "Accesso effettuato a %s come %s%s\n", host, user, where)
	return nil
}

func sourceLabel(src string) string {
	if src == config.SourceKeyring {
		return "portachiavi di sistema"
	}
	return "file di configurazione"
}

// loginHost: --hostname, poi GS_HOST, poi la richiesta interattiva.
func loginHost(f *cmdutil.Factory) (string, error) {
	h := normalizeHost(f.HostnameFlag)
	if h == "" {
		h = normalizeHost(f.Getenv(cmdutil.EnvHost))
	}
	if h != "" {
		return h, nil
	}
	if !f.IO.InTTY {
		return "", cmdutil.UsageErrorf("istanza mancante: usa --hostname o %s", cmdutil.EnvHost)
	}
	def := ""
	if cfg, err := f.Config(); err == nil {
		def, _ = cfg.DefaultHost()
	}
	prompt := "Istanza di GitStack (host[:porta]): "
	if def != "" {
		prompt = fmt.Sprintf("Istanza di GitStack (host[:porta]) [%s]: ", def)
	}
	_, _ = fmt.Fprint(f.IO.ErrOut, prompt)
	line, err := readLine(f.IO.In)
	if err != nil && line == "" {
		return "", cmdutil.UsageErrorf("istanza mancante")
	}
	h = normalizeHost(line)
	if h == "" {
		h = def
	}
	if h == "" {
		return "", cmdutil.UsageErrorf("istanza mancante")
	}
	return h, nil
}

// verifyToken chiede all'API chi è il proprietario del token e ne restituisce
// lo username. Un token rifiutato dà exit 4 (APIError 401).
func verifyToken(ctx context.Context, f *cmdutil.Factory, host, token string) (string, error) {
	cl, err := api.New(api.Options{Host: host, Token: token, Version: f.Version, HTTP: f.HTTPClient()})
	if err != nil {
		return "", err
	}
	resp, err := cl.Generated().GetCurrentSessionWithResponse(ctx)
	if err != nil {
		return "", fmt.Errorf("istanza %s non raggiungibile: %w", host, scrub(err, token))
	}
	if err := api.CheckStatus(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", fmt.Errorf("risposta inattesa da %s (HTTP %d)", host, resp.StatusCode())
	}
	return string(resp.JSON200.User.Username), nil
}

// scrub toglie il token da un errore, per non scriverlo mai in un messaggio.
func scrub(err error, token string) error {
	if token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), token, "***"))
}

// readLine legge una riga un byte alla volta, così più richieste di fila sullo
// stesso stdin (istanza, poi token) non si portano via i dati l'una dell'altra.
func readLine(r io.Reader) (string, error) {
	var sb strings.Builder
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				return sb.String(), nil
			}
			sb.WriteByte(b[0])
		}
		if err != nil {
			return sb.String(), err
		}
	}
}

func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxTokenInput))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// promptSecret legge un segreto senza fare eco quando stdin è un terminale
// vero; con un lettore qualunque (test) legge una riga.
func promptSecret(s *cmdutil.IOStreams, prompt string) (string, error) {
	_, _ = fmt.Fprint(s.ErrOut, prompt)
	if file, ok := s.In.(*os.File); ok {
		b, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(s.ErrOut)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, err := readLine(s.In)
	if err != nil && line == "" {
		return "", cmdutil.UsageErrorf("token mancante")
	}
	return line, nil
}
