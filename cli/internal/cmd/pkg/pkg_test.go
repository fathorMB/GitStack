package pkg_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

const list = `{"items":[{"name":"alice/app","tags":["v1","latest"],"sizeBytes":1024,"updatedAt":"2026-10-01T00:00:00Z","digest":"sha256:abc","signature":"verified","hasSbom":true},{"name":"alice/app/web","tags":[],"sizeBytes":5,"updatedAt":"2026-10-02T00:00:00Z","signature":"unverified","hasSbom":false}]}`

func run(t *testing.T, h http.HandlerFunc, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/meta") {
			_, _ = io.WriteString(w, `{"version":"dev"}`)
			return
		}
		h(w, r)
	}))
	defer srv.Close()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("dev", io_)
	cfg := t.TempDir()
	f.Getenv = func(k string) string {
		return map[string]string{"GS_HOST": srv.URL, "GS_TOKEN": "gst_x", config.EnvConfigDir: cfg}[k]
	}
	f.Git = noGit{}
	f.HTTPClient = srv.Client
	return root.Run(context.Background(), f, args), out.String(), errOut.String()
}

func TestPackageListJSON(t *testing.T) {
	var path string
	h := func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, list)
	}
	c, o, e := run(t, h, "package", "list", "alice/app", "--json", "name,signature,hasSbom")
	if c != 0 || path != "/api/v1/repos/alice/app/packages" {
		t.Fatalf("list --json: %d %q %q", c, path, e)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(o), &got); err != nil || len(got) != 2 {
		t.Fatalf("json non valido: %v %q", err, o)
	}
	if got[0]["signature"] != "verified" || got[0]["hasSbom"] != true || got[1]["signature"] != "unverified" {
		t.Fatalf("campi inattesi: %v", got)
	}
	if _, ok := got[0]["tags"]; ok {
		t.Fatalf("--json deve limitare i campi: %v", got[0])
	}
}

func TestPackageListTabella(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, list)
	}
	c, o, e := run(t, h, "package", "ls", "-R", "alice/app")
	if c != 0 || !strings.Contains(o, "alice/app/web") || !strings.Contains(o, "v1,latest") || !strings.Contains(o, "verified") {
		t.Fatalf("list: %d %q %q", c, o, e)
	}
}

func TestPackageDelete(t *testing.T) {
	var method, rawQuery string
	h := func(w http.ResponseWriter, r *http.Request) {
		method, rawQuery = r.Method, r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}
	if c, _, e := run(t, h, "package", "delete", "alice/app", "alice/app"); c != 2 || method != "" {
		t.Fatalf("senza --yes e senza TTY deve fallire con 2 e non chiamare l'API: %d %q %q", c, method, e)
	}
	if c, o, e := run(t, h, "package", "delete", "alice/app", "alice/app", "--tag", "v1", "-y"); c != 0 || method != "DELETE" || rawQuery != "name=alice%2Fapp&tag=v1" || !strings.Contains(o, "tag v1") {
		t.Fatalf("delete --tag: %d %s %q %q %q", c, method, rawQuery, o, e)
	}
	if c, _, e := run(t, h, "package", "delete", "alice/app", "alice/app", "--yes"); c != 0 || rawQuery != "name=alice%2Fapp" {
		t.Fatalf("delete: %d %q %q", c, rawQuery, e)
	}
}

func TestPackageDeleteProtetto(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"tag protetto"}`)
	}
	if c, _, _ := run(t, h, "package", "delete", "alice/app", "alice/app", "--tag", "v1", "-y"); c == 0 {
		t.Fatal("un 409 deve dare un errore")
	}
}
