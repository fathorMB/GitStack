package cmdutil

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// EnvHost seleziona l'istanza fuori da un repo.
const EnvHost = "GS_HOST"

// Factory è quello che un comando riceve per lavorare. I campi funzione si
// risolvono pigramente, così `gs version` o `gs --help` non toccano né il
// disco né git.
type Factory struct {
	IO      *IOStreams
	Version string

	// Valori dei flag globali --hostname e --repo (legati dalla radice).
	HostnameFlag string
	RepoFlag     string

	Getenv func(string) string
	Git    gsrepo.Git

	// Config apre la configurazione; Tokens dà il TokenSource (con GS_TOKEN
	// prioritario).
	Config func() (*config.Config, error)
	Tokens func() (config.TokenSource, error)

	// HTTPClient è il client HTTP di base; nei test si sostituisce.
	HTTPClient func() *http.Client
}

// New costruisce la Factory di produzione.
func New(version string, io *IOStreams) *Factory {
	f := &Factory{
		IO:      io,
		Version: version,
		Getenv:  os.Getenv,
		Git:     gsrepo.ExecGit{},
	}
	var cfg *config.Config
	f.Config = func() (*config.Config, error) {
		if cfg != nil {
			return cfg, nil
		}
		c, err := config.Load(f.Getenv)
		if err != nil {
			return nil, err
		}
		cfg = c
		return cfg, nil
	}
	f.Tokens = func() (config.TokenSource, error) {
		c, err := f.Config()
		if err != nil {
			return nil, err
		}
		return config.WithEnv(config.FileTokens{Cfg: c}, f.Getenv), nil
	}
	f.HTTPClient = func() *http.Client { return &http.Client{Timeout: 60 * time.Second} }
	return f
}

// Host sceglie l'istanza. Ordine: --hostname; host del remote origin se si è
// in un repo; GS_HOST; istanza predefinita della configurazione. Nessuna →
// errore di non autenticato (exit 4).
func (f *Factory) Host() (string, error) {
	return ResolveHost(context.Background(), f.HostnameFlag, f.Getenv, f.Git, f.defaultHost)
}

func (f *Factory) defaultHost() (string, error) {
	c, err := f.Config()
	if err != nil {
		return "", err
	}
	return c.DefaultHost()
}

// BaseRepo sceglie owner/repo: --repo, altrimenti il remote origin.
func (f *Factory) BaseRepo() (owner, repo string, err error) {
	return ResolveRepo(context.Background(), f.RepoFlag, f.Git)
}

// Token dà il token per host (GS_TOKEN vince). Senza token → exit 4.
func (f *Factory) Token(host string) (string, error) {
	ts, err := f.Tokens()
	if err != nil {
		return "", err
	}
	tok, _, err := ts.Token(host)
	if errors.Is(err, config.ErrNoToken) {
		return "", NotAuthenticatedError("nessun token per %s: esegui `gs auth login` o imposta %s", host, config.EnvToken)
	}
	return tok, err
}

// ResolveHost è la regola di G7, separata dalla Factory per i test.
func ResolveHost(ctx context.Context, hostnameFlag string, getenv func(string) string, g gsrepo.Git, defaultHost func() (string, error)) (string, error) {
	if h := strings.TrimSpace(hostnameFlag); h != "" {
		return h, nil
	}
	if g != nil {
		if r, err := gsrepo.OriginRemote(ctx, g); err == nil {
			return r.Host, nil
		}
	}
	if h := strings.TrimSpace(getenv(EnvHost)); h != "" {
		return h, nil
	}
	if defaultHost != nil {
		h, err := defaultHost()
		if err != nil {
			return "", err
		}
		if h != "" {
			return h, nil
		}
	}
	return "", NotAuthenticatedError("nessuna istanza: usa --hostname, %s o `gs auth login`", EnvHost)
}

// ResolveRepo è la regola di G7 per owner/repo.
func ResolveRepo(ctx context.Context, repoFlag string, g gsrepo.Git) (owner, repo string, err error) {
	if v := strings.TrimSpace(repoFlag); v != "" {
		owner, repo, err = gsrepo.ParseRepoFlag(v)
		if err != nil {
			return "", "", &UsageError{Msg: err.Error()}
		}
		return owner, repo, nil
	}
	if g != nil {
		r, err := gsrepo.OriginRemote(ctx, g)
		if err == nil {
			return r.Owner, r.Repo, nil
		}
		if !errors.Is(err, gsrepo.ErrNoRemote) {
			return "", "", err
		}
	}
	return "", "", UsageErrorf("repo non determinabile: usa --repo owner/repo o lancia il comando in un repo con remote origin")
}
