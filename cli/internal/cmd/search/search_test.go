package search_test

import (
	"context"
	"fmt"
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

type result struct {
	code        int
	out, errOut string
	queries     []string
}

func run(t *testing.T, routes map[string]string, args ...string) result {
	t.Helper()
	var mu sync.Mutex
	var res result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/meta") {
			res.queries = append(res.queries, r.URL.Path+"?"+r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		body, ok := routes[strings.TrimPrefix(r.URL.Path, "/api/v1")]
		if !ok {
			w.WriteHeader(404)
			body = `{"error":{"code":"not_found","message":"non trovato"}}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", srv.URL, "-R", "acme/web"}, args...)
	res.code = root.Run(context.Background(), f, full)
	res.out, res.errOut = out.String(), errOut.String()
	return res
}

func issueJSON(n int, title string) string {
	return fmt.Sprintf(`{"issue":{"number":%d,"title":%q,"state":"open","author":{"id":"00000000-0000-0000-0000-000000000001","username":"alice","kind":"human"},"labels":[],"assignees":[],"locked":false,"commentCount":0,"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z"},"repo":"acme/web"}`, n, title)
}

func TestSearchIssues(t *testing.T) {
	routes := map[string]string{"/search/issues": `{"items":[` + issueJSON(3, "Crash") + `,` + issueJSON(5, "Altro") + `],"page":1,"perPage":30,"total":2}`}
	r := run(t, routes, "search", "issues", "is:open", "label:bug", "--sort", "updated")
	if r.code != 0 || !strings.Contains(r.out, "acme/web") || !strings.Contains(r.out, "#3") || !strings.Contains(r.out, "Crash") {
		t.Fatalf("%+v", r)
	}
	q := r.queries[0]
	if !strings.Contains(q, "q=is%3Aopen+label%3Abug") || !strings.Contains(q, "sort=updated") {
		t.Errorf("query: %s", q)
	}
	if strings.Contains(r.errOut, "Mostrate") {
		t.Errorf("avviso inatteso: %s", r.errOut)
	}
	j := run(t, routes, "search", "issues", "crash", "--json", "repo,number,url")
	if j.code != 0 || !strings.Contains(j.out, `"url":"`) || !strings.Contains(j.out, "/acme/web/issues/3") {
		t.Errorf("json: %+v", j)
	}
}

func TestSearchIssuesMostraSeTroncato(t *testing.T) {
	routes := map[string]string{"/search/issues": `{"items":[` + issueJSON(1, "Uno") + `],"page":1,"perPage":1,"total":40}`}
	r := run(t, routes, "search", "issues", "x", "-L", "1")
	if r.code != 0 || !strings.Contains(r.errOut, "Mostrate 1 issue su 40") {
		t.Errorf("%+v", r)
	}
}

func TestSearchIssuesUsoErrato(t *testing.T) {
	for _, args := range [][]string{{"search", "issues"}, {"search", "issues", "x", "--sort", "boh"}, {"search", "issues", "x", "-L", "0"}, {"search", "code", "x"}} {
		if r := run(t, nil, args...); r.code != 2 || len(r.queries) != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestSearchCode(t *testing.T) {
	routes := map[string]string{"/repos/acme/web/search": `{"query":"func","ref":"main","limitReached":false,"timedOut":false,"results":[{"path":"a/b.go","line":3,"fragment":"  func main() {"}]}`}
	r := run(t, routes, "search", "code", "func", "--ref", "main")
	if r.code != 0 || r.out != "a/b.go:3: func main() {\n" || r.errOut != "" {
		t.Fatalf("%+v", r)
	}
	if q := r.queries[0]; !strings.Contains(q, "q=func") || !strings.Contains(q, "ref=main") {
		t.Errorf("query: %s", q)
	}
	j := run(t, routes, "search", "code", "func", "--json", "results,limitReached")
	if !strings.Contains(j.out, `"limitReached":false`) || !strings.Contains(j.out, "/acme/web/blob/main/a/b.go#L3") {
		t.Errorf("json: %+v", j)
	}
}

func TestSearchCodeSegnalaIlLimite(t *testing.T) {
	routes := map[string]string{"/repos/acme/web/search": `{"query":"ab","ref":"main","limitReached":true,"timedOut":true,"results":[{"path":"x.txt","line":1,"fragment":"ab"}]}`}
	r := run(t, routes, "search", "code", "ab")
	if r.code != 0 || !strings.Contains(r.errOut, "limitati ai primi 100") || !strings.Contains(r.errOut, "interrotta dopo 10 secondi") {
		t.Errorf("%+v", r)
	}
	j := run(t, routes, "search", "code", "ab", "--json", "limitReached,timedOut")
	if !strings.Contains(j.out, `"limitReached":true`) || !strings.Contains(j.out, `"timedOut":true`) || !strings.Contains(j.errOut, "limitati ai primi 100") {
		t.Errorf("json: %+v", j)
	}
}

func TestSearchCodeErrori(t *testing.T) {
	if r := run(t, nil, "search", "code", "a"); r.code != 2 || len(r.queries) != 0 {
		t.Errorf("query corta: %+v", r)
	}
	if r := run(t, nil, "search", "code", "abc"); r.code != 6 {
		t.Errorf("404: %+v", r)
	}
}
