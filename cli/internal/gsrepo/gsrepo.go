// Package gsrepo ricava istanza e owner/repo dal remote origin del repo
// corrente (G7).
package gsrepo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Remote è un remote scomposto.
type Remote struct {
	Host  string // per HTTPS host[:porta]; per SSH l'host senza la porta SSH
	Owner string
	Repo  string
}

// FullName è owner/repo.
func (r Remote) FullName() string { return r.Owner + "/" + r.Repo }

// ErrNoRemote: non si è in un repo o origin non esiste.
var ErrNoRemote = errors.New("nessun remote origin")

// Git legge l'URL di un remote; in produzione esegue git, nei test si sostituisce.
type Git interface {
	RemoteURL(ctx context.Context, name string) (string, error)
}

// ExecGit esegue `git remote get-url <name>` in Dir ("" = cartella corrente).
type ExecGit struct{ Dir string }

// RemoteURL implementa Git. Qualunque fallimento (niente git, niente repo,
// niente remote) è ErrNoRemote: fuori da un repo gs deve poter funzionare.
func (g ExecGit) RemoteURL(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", name)
	cmd.Dir = g.Dir
	out, err := cmd.Output()
	if err != nil {
		return "", ErrNoRemote
	}
	u := strings.TrimSpace(string(out))
	if u == "" {
		return "", ErrNoRemote
	}
	return u, nil
}

// OriginRemote legge e interpreta il remote origin.
func OriginRemote(ctx context.Context, g Git) (Remote, error) {
	u, err := g.RemoteURL(ctx, "origin")
	if err != nil {
		return Remote{}, err
	}
	return ParseRemote(u)
}

// ParseRemote interpreta un URL di remote:
//
//	https://host[:porta]/owner/repo[.git]
//	git@host:owner/repo[.git]            (scp-like)
//	ssh://git@host[:2222]/owner/repo[.git]
//
// Per SSH la porta non fa parte dell'host: è la porta SSH, non quella HTTP.
func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Remote{}, ErrNoRemote
	}
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Remote{}, fmt.Errorf("remote %q: %w", raw, err)
		}
		switch u.Scheme {
		case "https", "http":
			host = u.Host
		case "ssh", "git+ssh", "ssh+git":
			host = u.Hostname()
		default:
			return Remote{}, fmt.Errorf("remote %q: schema %q non supportato", raw, u.Scheme)
		}
		path = u.Path
	} else {
		// scp-like: [user@]host:owner/repo
		at := raw
		if i := strings.Index(at, "@"); i >= 0 {
			at = at[i+1:]
		}
		i := strings.Index(at, ":")
		if i <= 0 {
			return Remote{}, fmt.Errorf("remote %q non riconosciuto", raw)
		}
		host, path = at[:i], at[i+1:]
	}
	owner, repo, err := splitPath(path)
	if err != nil {
		return Remote{}, fmt.Errorf("remote %q: %w", raw, err)
	}
	if host == "" {
		return Remote{}, fmt.Errorf("remote %q: host mancante", raw)
	}
	return Remote{Host: strings.ToLower(host), Owner: owner, Repo: repo}, nil
}

func splitPath(p string) (owner, repo string, err error) {
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("il percorso deve essere owner/repo")
	}
	return parts[0], parts[1], nil
}

// ParseRepoFlag interpreta il valore di --repo (owner/repo).
func ParseRepoFlag(v string) (owner, repo string, err error) {
	owner, repo, err = splitPath(strings.TrimSpace(v))
	if err != nil {
		return "", "", fmt.Errorf("--repo %q: %w", v, err)
	}
	return owner, repo, nil
}
