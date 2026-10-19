package ci_test

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
)

const runID = "00000000-0000-4000-8000-000000000001"
const jobID = "00000000-0000-4000-8000-000000000002"

type result struct {
	code     int
	out, err string
}

func execute(t *testing.T, handler http.HandlerFunc, args ...string) result {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	f.Getenv = func(k string) string {
		switch k {
		case config.EnvConfigDir:
			return t.TempDir()
		case config.EnvToken:
			return "token"
		case "GS_HOST":
			return srv.URL
		case "GS_NO_KEYRING":
			return "1"
		}
		return ""
	}
	f.HTTPClient = srv.Client
	full := append([]string{"--hostname", srv.URL, "-R", "alice/repo"}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

func api(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/meta" {
			w.WriteHeader(404)
			return
		}
		handler(w, r)
	}
}

func TestComandiCIJSON(t *testing.T) {
	cases := []struct {
		name                         string
		args                         []string
		method, path, body, response string
	}{
		{"run list", []string{"run", "list", "--json", "id,status"}, "GET", "/api/v1/repos/alice/repo/actions/runs", "", `{"items":[{"id":"` + runID + `","status":"completed","conclusion":"success","workflowId":"build"}]}`},
		{"run view", []string{"run", "view", runID, "--json", "id,status"}, "GET", "/api/v1/repos/alice/repo/actions/runs/" + runID, "", `{"id":"` + runID + `","status":"completed","conclusion":"success","workflowId":"build"}`},
		{"run rerun", []string{"run", "rerun", runID, "--json", "id,status"}, "POST", "/api/v1/repos/alice/repo/actions/runs/" + runID + "/rerun", "", `{"id":"` + runID + `","status":"queued","workflowId":"build"}`},
		{"run cancel", []string{"run", "cancel", runID, "--json", "id,status"}, "POST", "/api/v1/repos/alice/repo/actions/runs/" + runID + "/cancel", "", `{"id":"` + runID + `","status":"cancelled","workflowId":"build"}`},
		{"workflow list", []string{"workflow", "list", "--json", "id,name"}, "GET", "/api/v1/repos/alice/repo/workflows", "", `{"items":[{"id":"build","name":"Build","path":".gitstack/workflows/build.yml","state":"active"}]}`},
		{"workflow dispatch", []string{"workflow", "run", "build.yml", "--ref", "main", "--input", "flavor=fast", "--json", "id,status"}, "POST", "/api/v1/repos/alice/repo/workflows/build.yml/dispatches", `{"inputs":{"flavor":"fast"},"ref":"main"}`, `{"id":"` + runID + `","status":"queued","workflowId":"build"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := execute(t, api(func(w http.ResponseWriter, req *http.Request) {
				b, _ := io.ReadAll(req.Body)
				if req.Method != tc.method || req.URL.Path != tc.path {
					t.Errorf("richiesta %s %s", req.Method, req.URL.Path)
				}
				if tc.body != "" && string(b) != tc.body {
					t.Errorf("body %s", b)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(map[string]int{"POST": 202, "GET": 200}[tc.method])
				_, _ = io.WriteString(w, tc.response)
			}), tc.args...)
			if r.code != 0 || !strings.Contains(r.out, `"id"`) {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestWatchLogsExitAndErrorCodes(t *testing.T) {
	var calls int
	r := execute(t, api(func(w http.ResponseWriter, req *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"`+runID+`","status":"completed","conclusion":"failure","workflowId":"build","jobs":[{"id":"`+jobID+`","name":"test","status":"completed","conclusion":"failure"}]}`)
	}), "run", "watch", runID, "--interval", "1ms", "--log-failed", "--exit-status", "--json", "id,status")
	if r.code != 1 || !strings.Contains(r.out, `"status":"completed"`) || calls != 2 {
		t.Fatalf("watch: %+v, chiamate %d", r, calls)
	}
	logs := execute(t, api(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(req.URL.Path, "/logs") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "errore test")
			return
		}
		_, _ = io.WriteString(w, `{"id":"`+runID+`","status":"completed","conclusion":"failure","workflowId":"build","jobs":[{"id":"`+jobID+`","name":"test","status":"completed","conclusion":"failure"}]}`)
	}), "run", "view", runID, "--log-failed")
	if logs.code != 0 || !strings.Contains(logs.out, "errore test") {
		t.Fatalf("log: %+v", logs)
	}
	missing := execute(t, api(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"manca"}}`)
	}), "workflow", "list", "--json", "id")
	if missing.code != 6 || !strings.Contains(missing.err, `"code":"not_found"`) {
		t.Fatalf("404: %+v", missing)
	}
	bad := execute(t, api(func(w http.ResponseWriter, _ *http.Request) { t.Fatal("la richiesta non deve partire") }), "run", "view", "nope", "--json", "id")
	if bad.code != 2 {
		t.Fatalf("uso: %+v", bad)
	}
	for _, tc := range []struct{ status, want int }{{401, 4}, {403, 5}, {409, 1}} {
		r := execute(t, api(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, `{"error":{"code":"api_error","message":"negato"}}`)
		}), "run", "list", "--json", "id")
		if r.code != tc.want || !strings.Contains(r.err, `"error"`) || r.out != "" {
			t.Errorf("HTTP %d: %+v", tc.status, r)
		}
	}
}
