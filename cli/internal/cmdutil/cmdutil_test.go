package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"silent", ErrSilent, 0},
		{"generico", errors.New("boom"), 1},
		{"uso errato", UsageErrorf("flag"), 2},
		{"401", &APIError{Status: 401, Code: "unauthenticated"}, 4},
		{"401 credenziali", &APIError{Status: 401, Code: "invalid_credentials"}, 4},
		{"403", &APIError{Status: 403, Code: "forbidden"}, 5},
		{"403 insufficient_scope", &APIError{Status: 403, Code: "insufficient_scope"}, 5},
		{"insufficient_scope senza stato", &APIError{Code: "insufficient_scope"}, 5},
		{"404", &APIError{Status: 404, Code: "not_found"}, 6},
		{"400", &APIError{Status: 400, Code: "bad_request"}, 1},
		{"409", &APIError{Status: 409, Code: "conflict"}, 1},
		{"422", &APIError{Status: 422, Code: "validation_failed"}, 1},
		{"500", &APIError{Status: 500}, 1},
		{"avvolto", fmt.Errorf("ctx: %w", &APIError{Status: 404}), 6},
		{"non autenticato locale", NotAuthenticatedError("manca"), 4},
		{"annullato", ErrCancelled, 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s: ExitCode = %d, atteso %d", c.name, got, c.want)
		}
	}
}

func TestErrorCode(t *testing.T) {
	if got := ErrorCode(&APIError{Status: 403, Code: "insufficient_scope"}); got != "insufficient_scope" {
		t.Errorf("codice API non conservato: %q", got)
	}
	if got := ErrorCode(UsageErrorf("x")); got != "usage_error" {
		t.Errorf("usage: %q", got)
	}
	if got := ErrorCode(&APIError{Status: 404}); got != "not_found" {
		t.Errorf("404 senza codice: %q", got)
	}
	if got := ErrorCode(errors.New("x")); got != "error" {
		t.Errorf("generico: %q", got)
	}
}

func TestConfirmOrYes(t *testing.T) {
	cases := []struct {
		name     string
		tty      bool
		yes      bool
		input    string
		expected string
		wantErr  bool
		wantCode int
		prompted bool
	}{
		{name: "--yes senza TTY", yes: true},
		{name: "--yes con TTY non chiede", tty: true, yes: true},
		{name: "senza TTY e senza --yes", wantErr: true, wantCode: 2},
		{name: "TTY, y", tty: true, input: "y\n"},
		{name: "TTY, yes", tty: true, input: "YES\n"},
		{name: "TTY, no", tty: true, input: "n\n", wantErr: true, wantCode: 1, prompted: true},
		{name: "TTY, EOF", tty: true, input: "", wantErr: true, wantCode: 1, prompted: true},
		{name: "TTY, testo atteso giusto", tty: true, input: "acme/api\n", expected: "acme/api", prompted: true},
		{name: "TTY, testo atteso sbagliato", tty: true, input: "acme/other\n", expected: "acme/api", wantErr: true, wantCode: 1, prompted: true},
		{name: "TTY, testo atteso: y non basta", tty: true, input: "y\n", expected: "acme/api", wantErr: true, wantCode: 1, prompted: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			io, in, _, errOut := Test()
			io.InTTY = c.tty
			in.WriteString(c.input)
			err := ConfirmOrYes(io, c.yes, "Confermi?", c.expected)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr && ExitCode(err) != c.wantCode {
				t.Errorf("exit = %d, atteso %d (%v)", ExitCode(err), c.wantCode, err)
			}
			if c.prompted && !strings.Contains(errOut.String(), "Confermi?") {
				t.Errorf("prompt non scritto su stderr: %q", errOut.String())
			}
			if !c.tty && in.Len() != len(c.input) {
				t.Error("senza TTY non si deve leggere stdin")
			}
		})
	}
}

type fakeGit struct {
	url string
	err error
}

func (f fakeGit) RemoteURL(context.Context, string) (string, error) { return f.url, f.err }

func noDefault() (string, error) { return "", nil }

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveHost(t *testing.T) {
	ctx := context.Background()
	inRepo := fakeGit{url: "https://git.acme.test/acme/api.git"}
	noRepo := fakeGit{err: gsrepo.ErrNoRemote}
	def := func() (string, error) { return "default.test", nil }
	cases := []struct {
		name    string
		flag    string
		env     map[string]string
		git     fakeGit
		def     func() (string, error)
		known   []string
		want    string
		wantErr bool
	}{
		{"flag vince su remote ed env", "forced.test", map[string]string{"GS_HOST": "env.test"}, inRepo, def, []string{"git.acme.test"}, "forced.test", false},
		{"remote origin configurato", "", nil, inRepo, def, []string{"git.acme.test"}, "git.acme.test", false},
		{"remote origin è la predefinita", "", nil, inRepo, func() (string, error) { return "git.acme.test", nil }, nil, "git.acme.test", false},
		{"GS_HOST vince sul remote", "", map[string]string{"GS_HOST": "env.test"}, inRepo, def, []string{"git.acme.test"}, "env.test", false},
		{"remote non configurato (github): predefinita", "", nil, fakeGit{url: "https://github.com/o/r.git"}, def, nil, "default.test", false},
		{"remote non configurato, niente predefinita: exit 4", "", nil, fakeGit{url: "https://github.com/o/r.git"}, noDefault, nil, "", true},
		{"remote SSH scp-like configurato", "", nil, fakeGit{url: "git@ssh.acme.test:acme/api.git"}, def, []string{"ssh.acme.test"}, "ssh.acme.test", false},
		{"remote ssh:// 2222 con istanza con porta HTTP", "", nil, fakeGit{url: "ssh://git@ssh.acme.test:2222/acme/api.git"}, def, []string{"ssh.acme.test:8443"}, "ssh.acme.test:8443", false},
		{"fuori da un repo: GS_HOST", "", map[string]string{"GS_HOST": "env.test"}, noRepo, def, nil, "env.test", false},
		{"fuori da un repo: predefinita", "", nil, noRepo, def, nil, "default.test", false},
		{"remote non riconosciuto: GS_HOST", "", map[string]string{"GS_HOST": "env.test"}, fakeGit{url: "/srv/git/x.git"}, def, nil, "env.test", false},
		{"niente: exit 4", "", nil, noRepo, noDefault, nil, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveHost(ctx, c.flag, envOf(c.env), c.git, c.def, func() []string { return c.known })
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Errorf("host = %q, atteso %q", got, c.want)
			}
			if c.wantErr && ExitCode(err) != ExitUnauthorized {
				t.Errorf("exit = %d, atteso 4", ExitCode(err))
			}
		})
	}
}

func TestResolveRepo(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		flag        string
		git         fakeGit
		owner, repo string
		wantCode    int
	}{
		{"flag", "acme/api", fakeGit{err: gsrepo.ErrNoRemote}, "acme", "api", 0},
		{"flag vince sul remote", "other/tool", fakeGit{url: "https://h.test/acme/api.git"}, "other", "tool", 0},
		{"HTTPS", "", fakeGit{url: "https://h.test/acme/api.git"}, "acme", "api", 0},
		{"HTTPS con porta", "", fakeGit{url: "https://h.test:8443/acme/api"}, "acme", "api", 0},
		{"SSH scp-like", "", fakeGit{url: "git@h.test:acme/api.git"}, "acme", "api", 0},
		{"ssh:// con 2222", "", fakeGit{url: "ssh://git@h.test:2222/acme/api.git"}, "acme", "api", 0},
		{"flag malformato", "acme", fakeGit{}, "", "", 2},
		{"nessun remote", "", fakeGit{err: gsrepo.ErrNoRemote}, "", "", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, r, err := ResolveRepo(ctx, c.flag, c.git)
			if got := ExitCode(err); got != c.wantCode {
				t.Fatalf("exit = %d, atteso %d (%v)", got, c.wantCode, err)
			}
			if o != c.owner || r != c.repo {
				t.Errorf("repo = %s/%s, atteso %s/%s", o, r, c.owner, c.repo)
			}
		})
	}
}

func TestBaseRepo(t *testing.T) {
	// makeConfig scrive un config.yaml con default_host in una cartella temporanea.
	makeConfig := func(defaultHost string) *config.Config {
		dir := t.TempDir()
		if defaultHost != "" {
			data := []byte(fmt.Sprintf("default_host: %s\n", defaultHost))
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return config.New(dir)
	}

	io, _, _, _ := Test()

	// Test: default host diverso dal remote → errore che nomina i due host.
	t.Run("default non matcha remote: errore", func(t *testing.T) {
		cfg := makeConfig("homehub")
		f := &Factory{
			IO:         io,
			Getenv:     os.Getenv,
			Config:     func() (*config.Config, error) { return cfg, nil },
			Git:        fakeGit{url: "https://github.com/fathorMB/GitStack.git"},
			Tokens:     func() (config.TokenSource, error) { return nil, nil },
			HTTPClient: func() *http.Client { return nil },
		}
		// Lancia Host() prima di BaseRepo per verificare la risoluzione.
		host, hErr := f.Host()
		if hErr != nil {
			t.Fatalf("Host() err = %v", hErr)
		}
		if host != "homehub" {
			t.Fatalf("Host() = %q, atteso %q", host, "homehub")
		}

		o, r, err := f.BaseRepo()
		if err == nil {
			t.Fatal("errore atteso, ricevuto nil")
		}
		if got := ExitCode(err); got != ExitUsage {
			t.Fatalf("exit = %d, atteso %d", got, ExitUsage)
		}
		msg := err.Error()
		if !strings.Contains(msg, "github.com") {
			t.Errorf("errore non nomina github.com: %q", msg)
		}
		if !strings.Contains(msg, "homehub") {
			t.Errorf("errore non nomina homehub: %q", msg)
		}
		if !strings.Contains(msg, "--repo") {
			t.Errorf("errore non suggerisce --repo: %q", msg)
		}
		if o != "" || r != "" {
			t.Errorf("owner/repo = %q/%q, atteso vuoto", o, r)
		}
	})

	// Test: default host matcha remote (anche SSH con porta) → funziona.
	t.Run("remote matcha la predefinita: ok", func(t *testing.T) {
		cfg := makeConfig("homehub")
		f := &Factory{
			IO:         io,
			Getenv:     os.Getenv,
			Config:     func() (*config.Config, error) { return cfg, nil },
			Git:        fakeGit{url: "ssh://git@homehub:2222/acme/api.git"},
			Tokens:     func() (config.TokenSource, error) { return nil, nil },
			HTTPClient: func() *http.Client { return nil },
		}

		o, r, err := f.BaseRepo()
		if err != nil {
			t.Fatalf("nessun errore atteso, got = %v", err)
		}
		if o != "acme" || r != "api" {
			t.Errorf("repo = %s/%s, atteso acme/api", o, r)
		}
	})

	// Test: --repo forzato supera il controllo del remote.
	t.Run("--repo supera il remote mismatch", func(t *testing.T) {
		cfg := makeConfig("homehub")
		f := &Factory{
			IO:         io,
			Getenv:     os.Getenv,
			Config:     func() (*config.Config, error) { return cfg, nil },
			RepoFlag:   "acme/api",
			Git:        fakeGit{url: "https://github.com/fathorMB/GitStack.git"},
			Tokens:     func() (config.TokenSource, error) { return nil, nil },
			HTTPClient: func() *http.Client { return nil },
		}

		o, r, err := f.BaseRepo()
		if err != nil {
			t.Fatalf("nessun errore atteso, got = %v", err)
		}
		if o != "acme" || r != "api" {
			t.Errorf("repo = %s/%s, atteso acme/api", o, r)
		}
	})
}
