package blame_test

import (
	"context"
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

type reply struct {
	status int
	body   string
}

type result struct {
	code        int
	out, errOut string
	queries     []string
}

func run(t *testing.T, routes map[string]reply, args ...string) result {
	t.Helper()
	var mu sync.Mutex
	var res result
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/meta") {
			res.queries = append(res.queries, strings.TrimPrefix(r.URL.Path, "/api/v1")+"?"+r.URL.RawQuery)
		}
		rep, ok := routes[strings.TrimPrefix(r.URL.Path, "/api/v1")]
		if !ok {
			rep = reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = w.Write([]byte(rep.body))
	}))
	defer srv.Close()
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", srv.URL, "-R", "acme/web", "blame"}, args...)
	res.code = root.Run(context.Background(), f, full)
	res.out, res.errOut = out.String(), errOut.String()
	return res
}

const (
	human = `{"name":"Alice","email":"alice@example.com","date":"2026-10-01T10:00:00Z","user":{"id":"00000000-0000-0000-0000-000000000001","username":"alice","kind":"human"}}`
	agent = `{"name":"Botty","email":"botty@agents.example.com","date":"2026-10-02T10:00:00Z","user":{"id":"00000000-0000-0000-0000-000000000002","username":"botty","kind":"agent"}}`
	anon  = `{"name":"Zed","email":"zed@example.com","date":"2026-10-03T10:00:00Z"}`
)

func commit(sha, subject, author string) string {
	return `{"sha":"` + sha + `","subject":"` + subject + `","parents":[],"author":` + author + `,"committer":` + author + `}`
}

var routes = map[string]reply{
	"/repos/acme/web/blame": {200, `{"path":"a.go","ref":"main","ranges":[` +
		`{"startLine":1,"endLine":2,"commit":` + commit("1111111111111111", "primo", human) + `},` +
		`{"startLine":3,"endLine":3,"commit":` + commit("2222222222222222", "bot", agent) + `},` +
		`{"startLine":4,"endLine":4,"commit":` + commit("3333333333333333", "esterno", anon) + `}]}`},
	"/repos/acme/web/contents": {200, `{"path":"a.go","content":"uno\ndue\ntre\nquattro\n","binary":false,"display":"highlight"}`},
	"/repos/acme/web/commits": {200, `{"items":[` + commit("2222222222222222", "modifica del bot", agent) + `,` + commit("1111111111111111", "primo", human) + `],"hasMore":true,"page":1,"perPage":30}`},
}

func TestBlameMostraAutoreEBadgeAgent(t *testing.T) {
	r := run(t, routes, "a.go")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(strings.TrimSuffix(r.out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("righe: %q", r.out)
	}
	for i, want := range []string{"11111111 alice", "11111111 alice", "22222222 botty [agent]", "33333333 Zed"} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("riga %d: %q, voluto prefisso %q", i+1, lines[i], want)
		}
	}
	if !strings.HasSuffix(lines[2], ") tre") || !strings.Contains(lines[2], "2026-10-02") {
		t.Errorf("riga 3: %q", lines[2])
	}
}

func TestBlameJSON(t *testing.T) {
	r := run(t, routes, "a.go", "--ref", "main", "--json", "path,ranges,url")
	if r.code != 0 || !strings.Contains(r.out, `"kind":"agent"`) || !strings.Contains(r.out, "/acme/web/blame/main/a.go") {
		t.Errorf("%+v", r)
	}
	for _, q := range r.queries {
		if strings.HasPrefix(q, "/repos/acme/web/contents") {
			t.Errorf("con --json non serve il contenuto: %v", r.queries)
		}
	}
}

func TestBlameFileTroppoGrande(t *testing.T) {
	rs := map[string]reply{"/repos/acme/web/blame": {400, `{"error":{"code":"blame_unavailable","message":"file binario o oltre 1 MB"}}`}}
	r := run(t, rs, "grande.txt")
	if r.code != 1 || !strings.Contains(r.errOut, "blame non disponibile per grande.txt") || !strings.Contains(r.errOut, "1 MB") {
		t.Errorf("%+v", r)
	}
}

func TestBlameFileInesistenteEUsoErrato(t *testing.T) {
	if r := run(t, nil, "no.go"); r.code != 6 {
		t.Errorf("404: %+v", r)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"a.go", "--author", "x"}, {"dir/"}} {
		if r := run(t, routes, args...); r.code != 2 || len(r.queries) != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestStorico(t *testing.T) {
	r := run(t, routes, "--history", "a.go", "--author", "botty", "-L", "2")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.out, "botty [agent]") || !strings.Contains(r.out, "modifica del bot") || !strings.Contains(r.out, "22222222") {
		t.Errorf("tabella: %s", r.out)
	}
	if q := r.queries[0]; !strings.Contains(q, "path=a.go") || !strings.Contains(q, "author=botty") {
		t.Errorf("query: %s", q)
	}
	if !strings.Contains(r.errOut, "Mostrati i primi 2 commit") {
		t.Errorf("avviso di troncamento: %q", r.errOut)
	}
	j := run(t, routes, "--history", "a.go", "--json", "sha,url")
	if !strings.Contains(j.out, "/acme/web/commit/2222222222222222") {
		t.Errorf("json: %+v", j)
	}
}
