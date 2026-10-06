package notification_test

import (
	"context"
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

const nid = "0b5c1f1e-8f0d-4c1e-9a2a-6f3f0d1b7a11"

const note = `{"id":"` + nid + `","reason":"assigned","read":false,"archived":false,"event":"issue.assigned","summary":"Assegnata a te",` +
	`"repository":{"id":"` + nid + `","fullName":"alice/web"},"issue":{"number":7,"title":"Crash","state":"open"},"createdAt":"2026-10-06T10:00:00Z"}`

func run(t *testing.T, h http.HandlerFunc, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
	code := root.Run(context.Background(), f, args)
	return code, out.String(), errOut.String()
}

func TestListFiltri(t *testing.T) {
	var q string
	code, out, errOut := run(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query().Get("state") + "|" + r.URL.Query().Get("reason") + "|" + r.URL.Query().Get("repo")
		_, _ = io.WriteString(w, `{"items":[`+note+`],"page":1,"perPage":30,"total":1,"unreadCount":1}`)
	}, "notification", "list", "--all", "--reason", "assigned,mentioned", "-R", "alice/web")
	if code != 0 || q != "all|assigned,mentioned|alice/web" || !strings.Contains(out, nid) || !strings.Contains(out, "#7") {
		t.Errorf("%d %q %q %s", code, q, out, errOut)
	}
	code, out, _ = run(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query().Get("state")
		_, _ = io.WriteString(w, `{"items":[`+note+`],"page":1,"perPage":30,"total":1,"unreadCount":1}`)
	}, "notification", "list", "--jq", ".[0].url")
	if code != 0 || q != "" || !strings.HasSuffix(strings.TrimSpace(out), "/alice/web/issues/7") {
		t.Errorf("default non lette: %d %q %q", code, q, out)
	}
}

func TestUsoErrato(t *testing.T) {
	for _, args := range [][]string{
		{"notification", "list", "--reason", "boh"},
		{"notification", "list", "--all", "--archived"},
		{"notification", "read"},
		{"notification", "read", "non-un-uuid"},
		{"notification", "read", nid, "--all"},
		{"notification", "view"},
		{"notification", "view", "xx"},
	} {
		code, _, errOut := run(t, func(http.ResponseWriter, *http.Request) { t.Error("chiamata di rete") }, args...)
		if code != 2 {
			t.Errorf("%v: exit %d (%s)", args, code, errOut)
		}
	}
}

func TestReadEView(t *testing.T) {
	var method, path, body string
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(b)
		if r.URL.Path == "/api/v1/notifications/read-all" {
			_, _ = io.WriteString(w, `{"marked":3}`)
			return
		}
		_, _ = io.WriteString(w, strings.Replace(note, `"read":false`, `"read":true`, 1))
	}
	if code, out, _ := run(t, h, "notification", "read", nid); code != 0 || method != "PATCH" || body != `{"read":true}` || !strings.Contains(out, nid) {
		t.Errorf("read: %d %s %s %q", code, method, body, out)
	}
	if code, out, _ := run(t, h, "notification", "read", "--all", "--reason", "assigned"); code != 0 || method != "POST" || !strings.Contains(out, "3 notifiche") {
		t.Errorf("read --all: %d %s %q", code, method, out)
	}
	code, out, _ := run(t, h, "notification", "view", nid)
	if code != 0 || method != "GET" || path != "/api/v1/notifications/"+nid || !strings.Contains(out, "Issue: #7 Crash (aperta)") || !strings.Contains(out, "/alice/web/issues/7") {
		t.Errorf("view: %d %q", code, out)
	}
	if code, _, _ := run(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"no"}}`)
	}, "notification", "view", nid); code != 6 {
		t.Errorf("404: exit %d", code)
	}
}
