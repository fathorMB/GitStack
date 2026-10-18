package mcp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

const requests = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"label_list","arguments":{"owner":"alice","repo":"web"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"api_request","arguments":{"method":"POST","path":"/repos/alice/web/labels","body":{"name":"x"}}}}
`

// run lancia `gs mcp` sulla radice vera, con un server finto come istanza.
func run(t *testing.T, args ...string) (code int, out, errOut string, hits []string) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/meta") {
			_, _ = io.WriteString(w, `{"version":"dev"}`)
			return
		}
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = io.WriteString(w, `[{"name":"bug"}]`)
	}))
	defer srv.Close()
	io_, in, o, e := cmdutil.Test()
	in.WriteString(requests)
	f := cmdutil.New("dev", io_)
	cfg := t.TempDir()
	f.Getenv = func(k string) string {
		return map[string]string{"GS_TOKEN": "gst_x", "GS_HOST": srv.URL, config.EnvConfigDir: cfg}[k]
	}
	f.Git = noGit{}
	f.HTTPClient = srv.Client
	args = append(args, "--host", srv.URL)
	code = root.Run(context.Background(), f, append([]string{"mcp"}, args...))
	return code, o.String(), e.String(), hits
}

func TestGsMcpParlaMCPSuStdout(t *testing.T) {
	code, out, errOut, hits := run(t)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 { // la notifica non ha risposta
		t.Fatalf("righe di risposta: %d\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, `{"jsonrpc":"2.0"`) {
			t.Errorf("stdout deve avere solo protocollo: %q", l)
		}
	}
	if !strings.Contains(lines[1], `[{\"name\":\"bug\"}]`) || strings.Contains(lines[1], `"isError":true`) {
		t.Errorf("label_list: %s", lines[1])
	}
	want := []string{"GET /api/v1/repos/alice/web/labels auth=Bearer gst_x", "POST /api/v1/repos/alice/web/labels auth=Bearer gst_x"}
	if strings.Join(hits, "|") != strings.Join(want, "|") {
		t.Errorf("richieste: %v", hits)
	}
}

func TestGsMcpReadOnly(t *testing.T) {
	code, out, errOut, hits := run(t, "--read-only")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[2], `"isError":true`) || !strings.Contains(lines[2], "read-only") {
		t.Errorf("api_request POST non rifiutato: %s", lines[2])
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "GET ") {
		t.Errorf("in sola lettura deve passare solo il GET: %v", hits)
	}
}

func TestGsMcpSenzaToken(t *testing.T) {
	io_, _, _, errOut := cmdutil.Test()
	f := cmdutil.New("dev", io_)
	f.Getenv = func(k string) string {
		return map[string]string{config.EnvConfigDir: t.TempDir(), "GS_HOST": "git.example.com", "GS_NO_KEYRING": "1"}[k]
	}
	f.Git = noGit{}
	if code := root.Run(context.Background(), f, []string{"mcp"}); code != cmdutil.ExitUnauthorized {
		t.Errorf("exit %d (%s), atteso %d", code, errOut.String(), cmdutil.ExitUnauthorized)
	}
}
