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
	return runVersions(t, "dev", "", args...)
}

// runVersions è run con la versione di gs e quella che l'istanza dichiara in /meta
// (vuota: /meta non la dà).
func runVersions(t *testing.T, gsVersion, serverVersion string, args ...string) (code int, out, errOut string, hits []string) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/meta") {
			_, _ = io.WriteString(w, `{"server_version":"`+serverVersion+`"}`)
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
	f := cmdutil.New(gsVersion, io_)
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

// M5 (G6): gs più vecchio dell'istanza nella stessa maggiore → il server parte con
// gli strumenti che conosce e avvisa su stderr; stdout resta solo protocollo.
func TestGsMcpMinorePiuVecchiaAvvisaEParte(t *testing.T) {
	code, out, errOut, hits := runVersions(t, "1.2.0", "1.3.0")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "avviso") || !strings.Contains(errOut, "1.2.0") || !strings.Contains(errOut, "1.3.0") ||
		!strings.Contains(errOut, "/downloads") {
		t.Errorf("manca l'avviso di aggiornamento su stderr: %q", errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || len(hits) != 2 {
		t.Fatalf("il server doveva servire le richieste: %d risposte, %d chiamate", len(lines), len(hits))
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, `{"jsonrpc":"2.0"`) {
			t.Errorf("stdout deve avere solo protocollo: %q", l)
		}
	}
}

// M5 (G6): versione maggiore diversa → non parte, spiega come aggiornare, e non
// scrive niente su stdout né chiama l'API.
func TestGsMcpMaggioreDiversaNonParte(t *testing.T) {
	code, out, errOut, hits := runVersions(t, "1.2.0", "2.0.0")
	if code != cmdutil.ExitGeneric {
		t.Errorf("exit %d, atteso %d", code, cmdutil.ExitGeneric)
	}
	if out != "" || len(hits) != 0 {
		t.Errorf("il server non doveva partire: stdout %q, chiamate %v", out, hits)
	}
	if !strings.Contains(errOut, "versione maggiore") || !strings.Contains(errOut, "/downloads") {
		t.Errorf("manca la spiegazione su come aggiornare: %q", errOut)
	}
}

func TestGsMcpStessaVersioneNessunAvviso(t *testing.T) {
	code, _, errOut, _ := runVersions(t, "1.2.0", "1.2.0")
	if code != 0 || errOut != "" {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
