package config

import "errors"

// EnvToken è la variabile d'ambiente che passa un token a gs senza login:
// vince su qualsiasi altra sorgente (CI, agenti).
const EnvToken = "GS_TOKEN"

// Sorgenti di un token, per `gs auth status`.
const (
	SourceEnv  = "GS_TOKEN"
	SourceFile = "file"
)

// ErrNoToken: nessun token per l'istanza.
var ErrNoToken = errors.New("nessun token per questa istanza")

// TokenSource è dove gs prende e conserva i token per istanza. GIT-164 vi
// aggiunge il portachiavi di sistema dietro la stessa interfaccia.
type TokenSource interface {
	// Token restituisce il token dell'host e il nome della sorgente
	// (SourceEnv, SourceFile, ...). ErrNoToken se manca.
	Token(host string) (token, source string, err error)
	// SetToken conserva il token dell'host.
	SetToken(host, token string) error
	// DeleteToken lo toglie; non è un errore se non c'è.
	DeleteToken(host string) error
}

// FileTokens conserva i token nel file hosts/<host>.yaml.
type FileTokens struct{ Cfg *Config }

func (f FileTokens) Token(host string) (string, string, error) {
	hc, ok, err := f.Cfg.Host(host)
	if err != nil {
		return "", "", err
	}
	if !ok || hc.Token == "" {
		return "", "", ErrNoToken
	}
	return hc.Token, SourceFile, nil
}

func (f FileTokens) SetToken(host, token string) error {
	hc, _, err := f.Cfg.Host(host)
	if err != nil {
		return err
	}
	hc.Token = token
	return f.Cfg.SaveHost(host, hc)
}

func (f FileTokens) DeleteToken(host string) error {
	hc, ok, err := f.Cfg.Host(host)
	if err != nil || !ok {
		return err
	}
	hc.Token = ""
	if err := f.Cfg.writeYAML(f.Cfg.HostPath(host), hc); err != nil {
		return err
	}
	return nil
}

// WithEnv antepone GS_TOKEN a inner: se la variabile è impostata, vince.
func WithEnv(inner TokenSource, getenv func(string) string) TokenSource {
	return envTokens{inner: inner, getenv: getenv}
}

type envTokens struct {
	inner  TokenSource
	getenv func(string) string
}

func (e envTokens) Token(host string) (string, string, error) {
	if t := e.getenv(EnvToken); t != "" {
		return t, SourceEnv, nil
	}
	return e.inner.Token(host)
}
func (e envTokens) SetToken(host, token string) error { return e.inner.SetToken(host, token) }
func (e envTokens) DeleteToken(host string) error     { return e.inner.DeleteToken(host) }
