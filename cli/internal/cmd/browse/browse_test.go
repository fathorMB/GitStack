package browse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/browse"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

func TestParseArg(t *testing.T) {
	cases := []struct {
		in   string
		want browse.Target
	}{
		{"", browse.Target{}},
		{"12", browse.Target{Issue: 12}},
		{"#7", browse.Target{Issue: 7}},
		{"cmd/main.go", browse.Target{Path: "cmd/main.go"}},
		{"cmd/main.go:42", browse.Target{Path: "cmd/main.go", Line: 42}},
		{"./README.md:3", browse.Target{Path: "README.md", Line: 3}},
		{"src/", browse.Target{Path: "src/"}},
		{".", browse.Target{}},
		{"a:b/c.txt", browse.Target{Path: "a:b/c.txt"}},
	}
	for _, c := range cases {
		got, err := browse.ParseArg(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseArg(%q) = %+v, %v; voluto %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"#0", "f.go:0", ":5"} {
		if _, err := browse.ParseArg(bad); err == nil {
			t.Errorf("ParseArg(%q) doveva fallire", bad)
		}
	}
}

func TestURL(t *testing.T) {
	const base = "https://git.example.com"
	cases := []struct {
		name string
		t    browse.Target
		want string
	}{
		{"repo", browse.Target{}, base + "/acme/web"},
		{"repo con ref", browse.Target{Ref: "feature/uno"}, base + "/acme/web/tree/feature/uno"},
		{"issue", browse.Target{Issue: 12}, base + "/acme/web/issues/12"},
		{"elenco issue", browse.Target{Issues: true}, base + "/acme/web/issues"},
		{"file", browse.Target{Path: "cmd/main.go", Ref: "main"}, base + "/acme/web/blob/main/cmd/main.go"},
		{"file con riga e ref con slash", browse.Target{Path: "cmd/main.go", Line: 42, Ref: "feature/uno"}, base + "/acme/web/blob/feature/uno/cmd/main.go#L42"},
		{"cartella", browse.Target{Path: "cmd/", Ref: "main"}, base + "/acme/web/tree/main/cmd"},
		{"percorso con spazi e #", browse.Target{Path: "doc/a b#1.md", Ref: "v1.0"}, base + "/acme/web/blob/v1.0/doc/a%20b%231.md"},
		{"storico del repo", browse.Target{History: true, Ref: "main"}, base + "/acme/web/commits/main"},
		{"storico di un file", browse.Target{History: true, Path: "README.md", Ref: "main"}, base + "/acme/web/commits/main/README.md"},
		{"blame", browse.Target{Blame: true, Path: "main.go", Line: 5, Ref: "main"}, base + "/acme/web/blame/main/main.go#L5"},
	}
	for _, c := range cases {
		got, err := browse.URL(base+"/", "acme", "web", c.t)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; voluto %q", c.name, got, err, c.want)
		}
	}
	for _, bad := range []browse.Target{{Blame: true, Ref: "main"}, {Blame: true, Path: "src/", Ref: "main"}, {History: true, Path: "f", Line: 3, Ref: "main"}} {
		if _, err := browse.URL(base, "acme", "web", bad); err == nil {
			t.Errorf("%+v: doveva fallire", bad)
		}
	}
}

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type result struct {
	code        int
	out, errOut string
	opened      []string
	repoCalls   int
}

// run lancia gs browse contro un gateway finto che conosce solo il repo.
func run(t *testing.T, args ...string) result {
	t.Helper()
	var mu sync.Mutex
	var res result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/repos/acme/web" {
			res.repoCalls++
			_, _ = w.Write([]byte(`{"defaultBranch":"trunk","fullName":"acme/web"}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no"}}`))
	}))
	defer srv.Close()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	f.OpenBrowser = func(u string) error { res.opened = append(res.opened, u); return nil }
	full := append([]string{"--hostname", srv.URL, "-R", "acme/web", "browse"}, args...)
	res.code = root.Run(context.Background(), f, full)
	res.out, res.errOut = out.String(), errOut.String()
	return res
}

func TestNoBrowserStampaLURL(t *testing.T) {
	r := run(t, "--no-browser")
	if r.code != 0 || len(r.opened) != 0 || !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web") || r.repoCalls != 0 {
		t.Errorf("repo: %+v", r)
	}
	// il ref predefinito viene dal repo
	r = run(t, "-n", "cmd/main.go:42")
	if !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web/blob/trunk/cmd/main.go#L42") || r.repoCalls != 1 {
		t.Errorf("file: %+v", r)
	}
	// con --branch niente chiamata
	r = run(t, "-n", "-b", "dev", "--history", "README.md")
	if !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web/commits/dev/README.md") || r.repoCalls != 0 {
		t.Errorf("storico: %+v", r)
	}
	r = run(t, "-n", "#12")
	if !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web/issues/12") || r.repoCalls != 0 {
		t.Errorf("issue: %+v", r)
	}
}

func TestApreIlBrowser(t *testing.T) {
	r := run(t, "--blame", "main.go")
	if r.code != 0 || len(r.opened) != 1 || !strings.HasSuffix(r.opened[0], "/acme/web/blame/trunk/main.go") || r.out != "" {
		t.Errorf("%+v", r)
	}
}

func TestUsoErrato(t *testing.T) {
	for _, args := range [][]string{
		{"--blame"},
		{"--blame", "--history", "f.go"},
		{"--issues", "f.go"},
		{"12", "--history"},
		{"a", "b"},
		{"f.go:0"},
	} {
		if r := run(t, args...); r.code != 2 || len(r.opened) != 0 || r.repoCalls != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
