package config

import (
	"errors"
	"strings"
)

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

// EnvHost è la variabile d'ambiente che sceglie l'istanza (GS_HOST).
const EnvHost = "GS_HOST"

// NormalizeHost porta un host a una forma confrontabile: minuscolo, senza
// schema né percorso.
func NormalizeHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// SameHost dice se due host (con eventuale porta) sono lo stesso.
func SameHost(a, b string) bool {
	a, b = NormalizeHost(a), NormalizeHost(b)
	return a != "" && a == b
}

// WithEnv antepone GS_TOKEN a inner, ma solo per l'istanza di GS_HOST o, se
// GS_HOST manca, per quella predefinita (defaultHost, può essere nil): GS_TOKEN
// non esce mai verso un altro host. Per gli altri host vale inner.
func WithEnv(inner TokenSource, getenv func(string) string, defaultHost func() string) TokenSource {
	return envTokens{inner: inner, getenv: getenv, defaultHost: defaultHost}
}

type envTokens struct {
	inner       TokenSource
	getenv      func(string) string
	defaultHost func() string
}

// EnvTokenTarget è l'host a cui vale GS_TOKEN: GS_HOST, altrimenti la predefinita.
func EnvTokenTarget(getenv func(string) string, defaultHost func() string) string {
	if h := NormalizeHost(getenv(EnvHost)); h != "" {
		return h
	}
	if defaultHost != nil {
		return NormalizeHost(defaultHost())
	}
	return ""
}

func (e envTokens) Token(host string) (string, string, error) {
	if t := e.getenv(EnvToken); t != "" && SameHost(host, EnvTokenTarget(e.getenv, e.defaultHost)) {
		return t, SourceEnv, nil
	}
	return e.inner.Token(host)
}
func (e envTokens) SetToken(host, token string) error { return e.inner.SetToken(host, token) }
func (e envTokens) DeleteToken(host string) error     { return e.inner.DeleteToken(host) }
