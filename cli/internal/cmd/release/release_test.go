package release_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

const rid = "0b5c1f1e-8f0d-4c1e-9a2a-6f3f0d1b7a11"
const aid = "1b5c1f1e-8f0d-4c1e-9a2a-6f3f0d1b7a11"
const rel = `{"id":"` + rid + `","tagName":"v1.0.0","name":"Uno","body":"note","draft":false,"prerelease":false,"latest":true,"createdAt":"2026-10-01T00:00:00Z","assets":[{"id":"` + aid + `","name":"in.bin","sha256":"d59386e0ae435e292fbe0ebcdb954b75ed5fb3922091277cb19f798fc5d50718","sizeBytes":5}]}`

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
func TestReleaseComandi(t *testing.T) {
	var method, path, body string
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.String(), string(b)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "download"):
			_, _ = io.WriteString(w, "asset")
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "assets"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"`+aid+`","name":"in.bin","sha256":"d59386e0ae435e292fbe0ebcdb954b75ed5fb3922091277cb19f798fc5d50718","sizeBytes":5}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "releases"):
			_, _ = io.WriteString(w, `{"items":[`+rel+`]}`)
		default:
			_, _ = io.WriteString(w, rel)
		}
	}
	if c, o, e := run(t, h, "release", "list", "alice/app"); c != 0 || !strings.Contains(o, "v1.0.0") {
		t.Fatalf("list: %d %q %q", c, o, e)
	}
	if c, _, e := run(t, h, "release", "create", "alice/app", "--tag", "v1.0.0", "--name", "Uno", "--notes", "note"); c != 0 || method != "POST" || !strings.Contains(path, "/repos/alice/app/releases") || !strings.Contains(body, `"tagName":"v1.0.0"`) {
		t.Fatalf("create: %d %s %s %q", c, method, path, e)
	}
	f := t.TempDir() + "/in.bin"
	if err := os.WriteFile(f, []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	if c, o, e := run(t, h, "release", "upload", rid, f, "alice/app"); c != 0 || method != "POST" || !strings.Contains(path, "name=in.bin") || !strings.Contains(o, aid) || !strings.Contains(o, "sha256:d59386e0ae435e292fbe0ebcdb954b75ed5fb3922091277cb19f798fc5d50718") {
		t.Fatalf("upload: %d %s %s %q", c, method, path, e)
	}
	out := t.TempDir() + "/out.bin"
	if c, _, e := run(t, h, "release", "download", rid, aid, "alice/app", "--output", out); c != 0 {
		t.Fatalf("download: %d %q", c, e)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "asset" {
		t.Fatalf("download=%q", b)
	}
}

func TestReleaseView(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, rel)
	}
	c, o, e := run(t, h, "release", "view", rid, "alice/app")
	if c != 0 {
		t.Fatalf("view: %d %q", c, e)
	}
	lines := strings.Split(strings.TrimRight(o, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "Uno (v1.0.0)" || lines[1] != "note" || !strings.Contains(lines[2], "in.bin") || !strings.Contains(lines[2], "5") || !strings.Contains(lines[2], "sha256:"+assetSHA) {
		t.Fatalf("output view inatteso: %q", o)
	}
}

func TestReleaseDownloadSHAErrato(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "download") {
			_, _ = io.WriteString(w, "altro")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, rel)
	}
	out := t.TempDir() + "/out.bin"
	if c, _, e := run(t, h, "release", "download", rid, aid, "alice/app", "--output", out); c == 0 || !strings.Contains(e, "SHA-256") {
		t.Fatalf("download con digest errato deve fallire: %d %q", c, e)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("il file non deve restare dopo un digest errato")
	}
}

const assetSHA = "d59386e0ae435e292fbe0ebcdb954b75ed5fb3922091277cb19f798fc5d50718"
