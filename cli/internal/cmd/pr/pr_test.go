package pr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/pr"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// Il server di test risponde secondo il contratto (api/openapi.yaml) per le
// rotte di gs pr; la chiave è `METODO /repos/...` senza /api/v1.

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type reply struct {
	code int
	body string
}

type handler func(r *http.Request, body []byte) reply

func fixed(code int, body string) handler {
	return func(*http.Request, []byte) reply { return reply{code, body} }
}

type call struct {
	method, path, query, body string
}

type res struct {
	code   int
	out    string
	errOut string
	calls  []call
	git    [][]string
	opened []string
}

type fakeGit struct{ url string }

func (g fakeGit) RemoteURL(context.Context, string) (string, error) {
	if g.url == "" {
		return "", gsrepo.ErrNoRemote
	}
	return g.url, nil
}

// run esegue gs con le rotte date. env sovrascrive l'ambiente di base (un
// valore vuoto toglie la variabile).
func run(t *testing.T, routes map[string]handler, env map[string]string, args ...string) res {
	t.Helper()
	var mu sync.Mutex
	var r res
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		path := strings.TrimPrefix(req.URL.Path, "/api/v1")
		if strings.HasSuffix(path, "/meta") {
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		r.calls = append(r.calls, call{req.Method, path, req.URL.RawQuery, string(body)})
		mu.Unlock()
		h, ok := routes[req.Method+" "+path]
		rep := reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		if ok {
			rep = h(req, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.code)
		_, _ = w.Write([]byte(rep.body))
	}))
	defer srv.Close()

	base := map[string]string{
		config.EnvConfigDir: t.TempDir(),
		config.EnvToken:     "tok",
		"GS_HOST":           srv.URL,
		"GS_NO_KEYRING":     "1",
	}
	for k, v := range env {
		base[k] = v
	}
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	f.Getenv = func(k string) string { return base[k] }
	f.Git = fakeGit{url: "https://srv.test/acme/web.git"}
	if u, ok := env["ORIGIN"]; ok {
		f.Git = fakeGit{url: u}
	}
	f.GitOutput = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "branch" {
			return "agent/12-crash", nil
		}
		return "", fmt.Errorf("git %v: non previsto", args)
	}
	f.RunGit = func(_ context.Context, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		r.git = append(r.git, args)
		return nil
	}
	f.OpenBrowser = func(u string) error {
		r.opened = append(r.opened, u)
		return nil
	}
	full := append([]string{"--hostname", srv.URL, "-R", "acme/web"}, args...)
	r.code = root.Run(context.Background(), f, full)
	r.out, r.errOut = out.String(), errOut.String()
	return r
}

func user(name string) string {
	return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-000000000001","username":%q,"kind":"human"}`, name)
}

func summaryJSON(n int, title string, draft bool) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"state":"open","draft":%t,"author":%s,"sourceBranch":"agent/12-crash","targetBranch":"main","reviewDecision":"required","requestedReviewers":[],"commentCount":0,"createdAt":"2026-10-10T10:00:00Z","updatedAt":"2026-10-11T10:00:00Z"}`, n, title, draft, user("alice"))
}

func listJSON(total int, items ...string) string {
	return fmt.Sprintf(`{"items":[%s],"page":1,"perPage":30,"total":%d}`, strings.Join(items, ","), total)
}

func prJSON(n int, title, state string, draft bool) string {
	// Tutti i campi di PullRequest, anche gli omitempty, perché il test dei campi li veda.
	return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-0000000000%02d","number":%d,"title":%q,"body":"Testo","state":%q,"draft":%t,"author":%s,"sourceBranch":"agent/12-crash","targetBranch":"main","headSha":%q,"baseSha":%q,"sourceBranchExists":true,"requestedReviewers":[{"id":"00000000-0000-0000-0000-000000000002","username":"bob","kind":"human"}],"reviewDecision":"required","approvals":0,"requiredApprovals":1,"statusState":"success","commentCount":0,"unresolvedThreadCount":0,"closingIssues":[12],"commitCount":2,"changedFiles":1,"additions":3,"deletions":1,"viaToken":{"id":"00000000-0000-0000-0000-000000000003","name":"agente-test"},"mergeMethod":"merge","mergeCommitSha":%q,"mergedBy":%s,"mergedAt":"2026-10-12T10:00:00Z","closedAt":"2026-10-12T09:00:00Z","createdAt":"2026-10-10T10:00:00Z","updatedAt":"2026-10-11T10:00:00Z"}`, n, n, title, state, draft, user("alice"), sha, sha, sha, user("bob"))
}

func reviewJSON(state string, counts bool) string {
	return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-000000000009","author":%s,"state":%q,"body":"","commitSha":%q,"stale":false,"countsAsApproval":%t,"commentCount":0,"submittedAt":"2026-10-12T10:00:00Z"}`, user("bob"), state, sha, counts)
}

func commentJSON(body string) string {
	return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-000000000005","author":%s,"body":%q,"deleted":false,"edited":false,"outdated":false,"createdAt":"2026-10-12T10:00:00Z","updatedAt":"2026-10-12T10:00:00Z"}`, user("alice"), body)
}

const mergeabilityJSON = `{"state":"mergeable","mergeable":true,"conflictFiles":[],"behindBy":0,"aheadBy":1,"blockers":[],"allowedMethods":["merge","squash"],"defaultMethod":"merge","approvals":0,"requiredApprovals":1,"headSha":"` + sha + `"}`

const filesJSON = `{"files":[{"path":"cli/x.go","status":"modified","additions":2,"deletions":1,"binary":false,"truncated":false,"patch":"@@ -1 +1 @@\n-a\n+b\n"}],"filesChanged":1,"additions":2,"deletions":1,"truncated":false,"viewedPaths":[],"headSha":"` + sha + `","baseSha":"` + sha + `"}`

func bodyOf(t *testing.T, c call) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(c.body), &m); err != nil {
		t.Fatalf("corpo %q: %v", c.body, err)
	}
	return m
}

func count(r res, method, path string) int {
	n := 0
	for _, c := range r.calls {
		if c.method == method && c.path == path {
			n++
		}
	}
	return n
}

const pr7 = "/repos/acme/web/pulls/7"

func TestListJSONEFiltri(t *testing.T) {
	routes := map[string]handler{
		"GET /repos/acme/web/pulls": fixed(200, listJSON(1, summaryJSON(3, "Crash", false))),
	}
	r := run(t, routes, nil, "pr", "list", "--state", "all", "--search", "review:required", "--json", "number,title,state,url")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	q := r.calls[len(r.calls)-1].query
	if !strings.Contains(q, "state=all") || !strings.Contains(q, "q=review%3Arequired") {
		t.Errorf("query: %s", q)
	}
	if !strings.Contains(r.out, `"number":3`) || !strings.Contains(r.out, "/acme/web/pulls/3") || strings.Contains(r.out, `"draft"`) {
		t.Errorf("json: %s", r.out)
	}
	// senza --state non si manda lo stato: lo decide il server (open)
	d := run(t, routes, nil, "pr", "list")
	if q := d.calls[len(d.calls)-1].query; strings.Contains(q, "state=") {
		t.Errorf("state inviato senza il flag: %s", q)
	}
	if !strings.Contains(d.out, "#3") || !strings.Contains(d.out, "agent/12-crash -> main") {
		t.Errorf("testo: %q", d.out)
	}
}

func TestListFiltriEUsoErrato(t *testing.T) {
	routes := map[string]handler{"GET /repos/acme/web/pulls": fixed(200, listJSON(0))}
	r := run(t, routes, nil, "pr", "list", "--head", "agent/12-crash", "--base", "main", "--sort", "comments", "--jq", "length")
	if r.code != 0 || !strings.Contains(r.calls[len(r.calls)-1].query, "head=agent%2F12-crash") || !strings.Contains(r.calls[len(r.calls)-1].query, "sort=comments") {
		t.Errorf("%+v", r)
	}
	for _, args := range [][]string{{"pr", "list", "--state", "boh"}, {"pr", "list", "--sort", "boh"}, {"pr", "list", "-L", "0"}, {"pr", "list", "extra"}} {
		if b := run(t, routes, nil, args...); b.code != 2 || count(b, "GET", "/repos/acme/web/pulls") != 0 {
			t.Errorf("%v: %+v", args, b)
		}
	}
}

func TestViewJSONTestoEWeb(t *testing.T) {
	routes := map[string]handler{"GET " + pr7: fixed(200, prJSON(7, "Correggi il crash", "open", true))}
	r := run(t, routes, nil, "pr", "view", "#7", "--json", "title,draft,state")
	if r.code != 0 || !strings.Contains(r.out, `"title":"Correggi il crash"`) || !strings.Contains(r.out, `"draft":true`) {
		t.Errorf("%+v", r)
	}
	txt := run(t, routes, nil, "pr", "view", "7")
	for _, want := range []string{"#7 Correggi il crash", "stato:    bozza", "agent/12-crash -> main", "revisori: bob", "chiude:   #12 al merge", "Testo"} {
		if !strings.Contains(txt.out, want) {
			t.Errorf("manca %q in:\n%s", want, txt.out)
		}
	}
	w := run(t, routes, nil, "pr", "view", "https://srv.test/acme/web/pulls/7", "--web")
	if w.code != 0 || len(w.opened) != 1 || !strings.HasSuffix(w.opened[0], "/acme/web/pulls/7") {
		t.Errorf("web: %+v", w)
	}
	if u := run(t, routes, nil, "pr", "view", "7", "--web", "--json", "title"); u.code != 2 {
		t.Errorf("--web con --json: %+v", u)
	}
	for _, args := range [][]string{{"pr", "view"}, {"pr", "view", "abc"}, {"pr", "view", "0"}} {
		if b := run(t, routes, nil, args...); b.code != 2 {
			t.Errorf("%v: exit %d", args, b.code)
		}
	}
}

func TestCodiciDiUscita(t *testing.T) {
	routes := map[string]handler{
		"GET /repos/acme/web/pulls/99": fixed(404, `{"error":{"code":"not_found","message":"Pull Request non trovata"}}`),
		"GET /repos/acme/web/pulls/98": fixed(403, `{"error":{"code":"insufficient_scope","message":"serve read"}}`),
		"GET /repos/acme/web/pulls/97": fixed(409, `{"error":{"code":"conflict","message":"conflitto"}}`),
	}
	if r := run(t, routes, nil, "pr", "view", "99", "--json", "title"); r.code != 6 || !strings.Contains(r.errOut, `"code":"not_found"`) || r.out != "" {
		t.Errorf("404: %+v", r)
	}
	if r := run(t, routes, nil, "pr", "view", "98"); r.code != 5 {
		t.Errorf("403: %+v", r)
	}
	if r := run(t, routes, nil, "pr", "view", "97"); r.code != 1 {
		t.Errorf("409: %+v", r)
	}
	if r := run(t, routes, map[string]string{config.EnvToken: ""}, "pr", "list"); r.code != 4 {
		t.Errorf("senza token: exit %d %s", r.code, r.errOut)
	}
}

func TestCreateJSONEBozza(t *testing.T) {
	routes := map[string]handler{
		"POST /repos/acme/web/pulls": func(_ *http.Request, body []byte) reply {
			return reply{201, prJSON(7, "Correggi il crash", "open", true)}
		},
	}
	r := run(t, routes, nil, "pr", "create", "--draft", "-t", "Correggi il crash", "-b", "fixes #12", "--reviewer", "@alice", "--reviewer", "alice", "--json", "number,draft,url")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	b := bodyOf(t, r.calls[0])
	if b["title"] != "Correggi il crash" || b["body"] != "fixes #12" || b["sourceBranch"] != "agent/12-crash" || b["draft"] != true {
		t.Errorf("corpo: %v", b)
	}
	if rs, _ := b["reviewers"].([]any); len(rs) != 1 || rs[0] != "alice" {
		t.Errorf("revisori senza duplicati e senza @: %v", b["reviewers"])
	}
	if !strings.Contains(r.out, `"number":7`) || !strings.Contains(r.out, "/acme/web/pulls/7") {
		t.Errorf("json: %s", r.out)
	}
	if t2 := run(t, routes, nil, "pr", "create", "-t", "x", "-H", "feat", "-B", "dev"); t2.code != 0 {
		t.Fatalf("%+v", t2)
	} else if b := bodyOf(t, t2.calls[0]); b["sourceBranch"] != "feat" || b["targetBranch"] != "dev" {
		t.Errorf("rami: %v", b)
	}
}

func TestCreateUsoErrato(t *testing.T) {
	routes := map[string]handler{"POST /repos/acme/web/pulls": fixed(201, prJSON(7, "x", "open", false))}
	for _, args := range [][]string{
		{"pr", "create"},
		{"pr", "create", "-t", "x", "-b", "a", "-F", "f.txt"},
		{"pr", "create", "-t", "x", "extra"},
		{"pr", "create", "-t", "x", "--web", "--json", "number"},
	} {
		if r := run(t, routes, nil, args...); r.code != 2 || count(r, "POST", "/repos/acme/web/pulls") != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestCreateConflittoEBodyFile(t *testing.T) {
	routes := map[string]handler{"POST /repos/acme/web/pulls": fixed(409, `{"error":{"code":"already_exists","message":"esiste già una PR"}}`)}
	if r := run(t, routes, nil, "pr", "create", "-t", "x", "--json", "number"); r.code != 1 || !strings.Contains(r.errOut, `"code":"already_exists"`) {
		t.Errorf("%+v", r)
	}
}

func TestReviewEsiti(t *testing.T) {
	routes := map[string]handler{
		"POST " + pr7 + "/reviews": fixed(201, reviewJSON("approved", true)),
	}
	r := run(t, routes, nil, "pr", "review", "7", "--approve", "--json", "state,countsAsApproval")
	if r.code != 0 || bodyOf(t, r.calls[0])["event"] != "approve" || !strings.Contains(r.out, `"countsAsApproval":true`) {
		t.Errorf("approve: %+v", r)
	}
	cr := map[string]handler{"POST " + pr7 + "/reviews": fixed(201, reviewJSON("commented", false))}
	c := run(t, cr, nil, "pr", "review", "7", "--comment", "-b", "Nota su x.go")
	if c.code != 0 || bodyOf(t, c.calls[0])["event"] != "comment" || !strings.Contains(c.out, "conta come approvazione: no") {
		t.Errorf("comment: %+v", c)
	}
	rc := run(t, routes, nil, "pr", "review", "7", "--request-changes", "-F", "-")
	if rc.code != 2 || count(rc, "POST", pr7+"/reviews") != 0 {
		t.Errorf("request-changes senza testo: %+v", rc)
	}
	for _, args := range [][]string{{"pr", "review", "7"}, {"pr", "review", "7", "--approve", "--comment"}} {
		if b := run(t, routes, nil, args...); b.code != 2 {
			t.Errorf("%v: exit %d", args, b.code)
		}
	}
	own := map[string]handler{"POST " + pr7 + "/reviews": fixed(403, `{"error":{"code":"own_pull_request","message":"PR propria"}}`)}
	if o := run(t, own, nil, "pr", "review", "7", "--approve"); o.code != 5 {
		t.Errorf("PR propria: exit %d", o.code)
	}
}

func TestCommentJSON(t *testing.T) {
	routes := map[string]handler{"POST " + pr7 + "/comments": fixed(201, commentJSON("ok"))}
	r := run(t, routes, nil, "pr", "comment", "7", "--body", "ok", "--json", "body,author")
	if r.code != 0 || bodyOf(t, r.calls[0])["body"] != "ok" || !strings.Contains(r.out, `"body":"ok"`) {
		t.Errorf("%+v", r)
	}
	if b := run(t, routes, nil, "pr", "comment", "7"); b.code != 2 || count(b, "POST", pr7+"/comments") != 0 {
		t.Errorf("senza testo: %+v", b)
	}
}

func TestReadyConRevisoriEUndo(t *testing.T) {
	routes := map[string]handler{
		"POST " + pr7 + "/ready":               fixed(200, prJSON(7, "x", "open", false)),
		"POST " + pr7 + "/requested-reviewers": fixed(200, prJSON(7, "x", "open", false)),
		"POST " + pr7 + "/draft":               fixed(200, prJSON(7, "x", "open", true)),
	}
	r := run(t, routes, nil, "pr", "ready", "7", "--reviewer", "alice", "--reviewer", "@bob")
	if r.code != 0 || len(r.calls) < 2 || r.calls[0].path != pr7+"/ready" || r.calls[1].path != pr7+"/requested-reviewers" {
		t.Fatalf("ordine: %+v", r)
	}
	if rs, _ := bodyOf(t, r.calls[1])["reviewers"].([]any); len(rs) != 2 || rs[1] != "bob" {
		t.Errorf("revisori: %v", r.calls[1].body)
	}
	u := run(t, routes, nil, "pr", "ready", "7", "--undo", "--json", "draft")
	if u.code != 0 || count(u, "POST", pr7+"/draft") != 1 || !strings.Contains(u.out, `"draft":true`) {
		t.Errorf("undo: %+v", u)
	}
	if b := run(t, routes, nil, "pr", "ready", "7", "--undo", "--reviewer", "x"); b.code != 2 {
		t.Errorf("--undo con --reviewer: exit %d", b.code)
	}
	conflict := map[string]handler{"POST " + pr7 + "/ready": fixed(409, `{"error":{"code":"not_draft","message":"non è una bozza"}}`)}
	if c := run(t, conflict, nil, "pr", "ready", "7", "--json", "draft"); c.code != 1 || !strings.Contains(c.errOut, `"code":"not_draft"`) {
		t.Errorf("not_draft: %+v", c)
	}
}

func TestMergeChiedeConfermaEMetodo(t *testing.T) {
	routes := map[string]handler{
		"POST " + pr7 + "/merge":       func(_ *http.Request, _ []byte) reply { return reply{200, prJSON(7, "x", "merged", false)} },
		"GET " + pr7 + "/mergeability": fixed(200, mergeabilityJSON),
	}
	// senza terminale e senza --yes: uso errato, niente unione
	if r := run(t, routes, nil, "pr", "merge", "7"); r.code != 2 || count(r, "POST", pr7+"/merge") != 0 {
		t.Fatalf("G8 senza --yes: %+v", r)
	}
	s := run(t, routes, nil, "pr", "merge", "7", "--squash", "--yes", "--delete-branch", "-t", "Titolo", "--match-head-commit", sha)
	if s.code != 0 || len(s.calls) != 1 {
		t.Fatalf("squash: %+v", s)
	}
	b := bodyOf(t, s.calls[0])
	if b["method"] != "squash" || b["deleteBranch"] != true || b["commitTitle"] != "Titolo" || b["expectedHeadSha"] != sha {
		t.Errorf("corpo: %v", b)
	}
	// senza metodo: il metodo predefinito del repo, letto dalla mergeability prima dell'unione
	d := run(t, routes, nil, "pr", "merge", "7", "-y", "--json", "state,mergedAt")
	if d.code != 0 || len(d.calls) != 2 || d.calls[0].method != "GET" || bodyOf(t, d.calls[1])["method"] != "merge" {
		t.Errorf("metodo predefinito: %+v", d)
	}
	if m := run(t, routes, nil, "pr", "merge", "7", "--merge", "--squash", "--yes"); m.code != 2 {
		t.Errorf("merge e squash insieme: exit %d", m.code)
	}
	c := map[string]handler{"POST " + pr7 + "/merge": fixed(409, `{"error":{"code":"approvals_required","message":"servono approvazioni"}}`)}
	if r := run(t, c, nil, "pr", "merge", "7", "--squash", "--yes", "--json", "state"); r.code != 1 || !strings.Contains(r.errOut, `"code":"approvals_required"`) {
		t.Errorf("409: %+v", r)
	}
}

func TestCloseChiedeConfermaECommento(t *testing.T) {
	routes := map[string]handler{
		"POST " + pr7 + "/close":    fixed(200, prJSON(7, "x", "closed", false)),
		"POST " + pr7 + "/comments": fixed(201, commentJSON("ciao")),
	}
	if r := run(t, routes, nil, "pr", "close", "7"); r.code != 2 || count(r, "POST", pr7+"/close") != 0 {
		t.Fatalf("G8 senza --yes: %+v", r)
	}
	r := run(t, routes, nil, "pr", "close", "7", "--yes", "--comment", "ciao", "--json", "state")
	if r.code != 0 || len(r.calls) != 2 || r.calls[0].path != pr7+"/close" || r.calls[1].path != pr7+"/comments" || bodyOf(t, r.calls[1])["body"] != "ciao" {
		t.Errorf("close: %+v", r)
	}
	if !strings.Contains(r.out, `"state":"closed"`) {
		t.Errorf("json: %s", r.out)
	}
	c := map[string]handler{"POST " + pr7 + "/close": fixed(409, `{"error":{"code":"already_closed","message":"già chiusa"}}`)}
	if x := run(t, c, nil, "pr", "close", "7", "-y"); x.code != 1 {
		t.Errorf("già chiusa: exit %d", x.code)
	}
}

func TestCheckoutScaricaIlRamo(t *testing.T) {
	routes := map[string]handler{"GET " + pr7: fixed(200, prJSON(7, "x", "open", false))}
	r := run(t, routes, nil, "pr", "checkout", "7", "--json", "sourceBranch")
	if r.code != 0 || len(r.git) != 2 {
		t.Fatalf("%+v", r)
	}
	if strings.Join(r.git[0], " ") != "fetch origin +refs/heads/agent/12-crash:refs/remotes/origin/agent/12-crash" || strings.Join(r.git[1], " ") != "switch agent/12-crash" {
		t.Errorf("git: %v", r.git)
	}
	if !strings.Contains(r.out, `"sourceBranch":"agent/12-crash"`) {
		t.Errorf("json: %s", r.out)
	}
	if o := run(t, routes, map[string]string{"ORIGIN": "https://srv.test/altro/repo.git"}, "pr", "checkout", "7"); o.code != 2 || len(o.git) != 0 {
		t.Errorf("origin diverso: %+v", o)
	}
	gone := map[string]handler{"GET " + pr7: fixed(200, strings.Replace(prJSON(7, "x", "merged", false), `"sourceBranchExists":true`, `"sourceBranchExists":false`, 1))}
	if g := run(t, gone, nil, "pr", "checkout", "7"); g.code != 6 || len(g.git) != 0 {
		t.Errorf("ramo eliminato: %+v", g)
	}
}

func TestDiffNameOnlyPatchEWhitespace(t *testing.T) {
	routes := map[string]handler{"GET " + pr7 + "/files": fixed(200, filesJSON)}
	n := run(t, routes, nil, "pr", "diff", "7", "--name-only")
	if n.code != 0 || n.out != "cli/x.go\n" {
		t.Errorf("name-only: %q %+v", n.out, n)
	}
	p := run(t, routes, nil, "pr", "diff", "7", "-w")
	if p.code != 0 || !strings.Contains(p.out, "-a\n+b\n") || !strings.Contains(p.calls[0].query, "ignoreWhitespace=true") {
		t.Errorf("patch: %q %+v", p.out, p)
	}
	j := run(t, routes, nil, "pr", "diff", "7", "--json", "files,filesChanged")
	if j.code != 0 || !strings.Contains(j.out, `"path":"cli/x.go"`) || !strings.Contains(j.out, `"filesChanged":1`) {
		t.Errorf("json: %s", j.out)
	}
	if u := run(t, routes, nil, "pr", "diff", "7", "--json", "nope"); u.code != 2 {
		t.Errorf("campo ignoto: exit %d", u.code)
	}
}

func TestStatusRamoEAutore(t *testing.T) {
	routes := map[string]handler{
		"GET /repos/acme/web/pulls": func(req *http.Request, _ []byte) reply {
			if strings.Contains(req.URL.RawQuery, "head=") {
				return reply{200, listJSON(1, summaryJSON(7, "Crash", false))}
			}
			return reply{200, listJSON(1, summaryJSON(9, "Altra", true))}
		},
	}
	r := run(t, routes, nil, "pr", "status", "--json", "current,authored")
	if r.code != 0 || !strings.Contains(r.out, `"title":"Crash"`) || !strings.Contains(r.out, `"title":"Altra"`) || strings.Contains(r.out, `"current":null`) {
		t.Errorf("json: %+v", r)
	}
	var qs []string
	for _, c := range r.calls {
		qs = append(qs, c.query)
	}
	if !strings.Contains(strings.Join(qs, "|"), "head=agent%2F12-crash") || !strings.Contains(strings.Join(qs, "|"), "author%3A%40me") {
		t.Errorf("query: %v", qs)
	}
	txt := run(t, routes, nil, "pr", "status")
	if !strings.Contains(txt.out, "Ramo corrente agent/12-crash: PR #7 Crash") || !strings.Contains(txt.out, "#9") {
		t.Errorf("testo: %q", txt.out)
	}
	none := map[string]handler{"GET /repos/acme/web/pulls": fixed(200, listJSON(0))}
	if e := run(t, none, nil, "pr", "status", "--json", "authored"); e.code != 0 || !strings.Contains(e.out, `"authored":[]`) {
		t.Errorf("vuoto: %+v", e)
	}
}

// Ogni campo di --json elencato per gs pr esiste nell'oggetto dell'API, e
// ogni campo è documentato in cli/README.md.
func TestCampiEDocumentazione(t *testing.T) {
	routes := map[string]handler{"GET " + pr7: fixed(200, prJSON(7, "x", "open", false))}
	r := run(t, routes, nil, "pr", "view", "7", "--jq", "keys[]")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	keys := map[string]bool{}
	for _, k := range strings.Fields(r.out) {
		keys[strings.Trim(k, `"`)] = true
	}
	for _, f := range pr.PRFields {
		if f != "url" && !keys[f] {
			t.Errorf("il campo %s di PRFields non c'è nell'oggetto PullRequest", f)
		}
	}
	b, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	all := append(append(append(append(append(append([]string{}, pr.PRFields...), pr.SummaryFields...), pr.ReviewFields...), pr.CommentFields...), pr.FilesFields...), pr.StatusFields...)
	for _, f := range all {
		if !strings.Contains(string(b), "`"+f+"`") {
			t.Errorf("il campo %s non è documentato in cli/README.md", f)
		}
	}
}
