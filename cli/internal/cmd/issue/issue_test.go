package issue_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/issue"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

// call è una richiesta ricevuta dal gateway finto.
type call struct {
	Method, Path, Query string
	Body                map[string]any
}

type reply struct {
	status int
	body   string
}

// fake è un gateway finto: risposte per "METODO percorso" (senza /api/v1).
type fake struct {
	mu     sync.Mutex
	routes map[string]reply
	calls  []call
	srv    *httptest.Server
}

func newFake(t *testing.T, routes map[string]reply) *fake {
	t.Helper()
	fk := &fake{routes: routes}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, r.URL.RawQuery, body})
		rep, ok := fk.routes[r.Method+" "+path]
		fk.mu.Unlock()
		if !ok {
			rep = reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			rep = reply{401, `{"error":{"code":"unauthenticated","message":"serve autenticazione"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

func (fk *fake) find(method, path string) []call {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	var out []call
	for _, c := range fk.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	code        int
	out, errOut string
}

func run(t *testing.T, fk *fake, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", fk.srv.URL, "-R", "acme/web"}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

const (
	alice  = `{"id":"00000000-0000-0000-0000-000000000001","username":"alice","kind":"human"}`
	issue7 = `{"id":"00000000-0000-0000-0000-0000000000a7","number":7,"title":"Crash","body":"stack trace","state":"open","author":` + alice +
		`,"labels":[{"id":"00000000-0000-0000-0000-0000000000b1","name":"bug","color":"d73a4a"}],"assignees":[` + alice + `],` +
		`"locked":false,"hidden":false,"edited":false,"commentCount":1,"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z"}`
	session = `{"authMethod":"token","mustChangePassword":false,"user":{"username":"alice","displayName":"Alice","kind":"human"}}`
)

func TestCreate_CampiETemplate(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET /auth/session":                      {200, session},
		"GET /repos/acme/web/issue-templates":    {200, `{"items":[{"name":"bug","title":"Bug: ","labels":["bug"],"body":"## Passi\n"}]}`},
		"GET /repos/acme/web/milestones":         {200, `{"items":[{"number":3,"title":"v1","description":"","state":"open","openIssues":0,"closedIssues":0,"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-01T10:00:00Z"}],"page":1,"perPage":100,"total":1}`},
		"POST /repos/acme/web/issues":            {201, issue7},
		"POST /repos/acme/web/issues/7/comments": {201, `{}`},
	})
	r := run(t, fk, "", "issue", "create", "-t", "Crash", "--template", "bug", "-l", "area,bug", "-a", "@me,bob", "-m", "V1")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	if got := strings.TrimSpace(r.out); got != fk.srv.URL+"/acme/web/issues/7" {
		t.Errorf("stdout = %q", got)
	}
	b := fk.find("POST", "/repos/acme/web/issues")[0].Body
	if b["title"] != "Crash" || b["body"] != "## Passi\n" || b["milestone"] != float64(3) {
		t.Errorf("corpo: %v", b)
	}
	if l, _ := json.Marshal(b["labels"]); string(l) != `["bug","area"]` {
		t.Errorf("etichette: %s", l)
	}
	if a, _ := json.Marshal(b["assignees"]); string(a) != `["alice","bob"]` {
		t.Errorf("assegnatari: %s", a)
	}
}

func TestCreate_TestoDaFileEStdin(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST /repos/acme/web/issues": {201, issue7}})
	dir := t.TempDir()
	p := filepath.Join(dir, "b.md")
	if err := os.WriteFile(p, []byte("da file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := run(t, fk, "", "issue", "create", "-t", "A", "-F", p); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, fk, "da stdin\n", "issue", "create", "-t", "B", "-F", "-"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	cs := fk.find("POST", "/repos/acme/web/issues")
	if cs[0].Body["body"] != "da file" || cs[1].Body["body"] != "da stdin\n" {
		t.Errorf("testi: %v %v", cs[0].Body, cs[1].Body)
	}
}

func TestCreate_UsoErrato(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /repos/acme/web/issue-templates": {200, `{"items":[{"name":"bug","body":"x"}]}`}})
	for _, args := range [][]string{
		{"issue", "create"}, // senza titolo
		{"issue", "create", "-t", "A", "-b", "x", "-F", "-"}, // body e file
		{"issue", "create", "-t", "A", "-T", "nuovo"},        // modello inesistente
		{"issue", "create", "-t", "A", "extra"},
	} {
		r := run(t, fk, "", args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d (%s)", args, r.code, r.errOut)
		}
	}
	if r := run(t, fk, "", "issue", "create", "-t", "A", "-T", "nuovo"); !strings.Contains(r.errOut, "bug") {
		t.Errorf("l'errore non elenca i modelli: %s", r.errOut)
	}
	if n := len(fk.find("POST", "/repos/acme/web/issues")); n != 0 {
		t.Errorf("create ha chiamato l'API %d volte", n)
	}
}

func TestList_FiltriERicerca(t *testing.T) {
	item := `{"number":7,"title":"Crash","state":"closed","closeReason":"duplicate","author":` + alice + `,"labels":[{"id":"00000000-0000-0000-0000-0000000000b1","name":"bug","color":"d73a4a"}],"assignees":[],"commentCount":0,"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z"}`
	fk := newFake(t, map[string]reply{"GET /repos/acme/web/issues": {200, `{"items":[` + item + `],"page":1,"perPage":30,"total":1}`}})
	r := run(t, fk, "", "issue", "list", "-l", "bug", "-l", "ui", "-a", "@me", "-A", "bob", "-m", "v1", "-S", "is:closed crash")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r.out != "#7\tclosed:duplicate\tbug\t2026-10-02\tCrash\n" {
		t.Errorf("tabella: %q", r.out)
	}
	q := fk.find("GET", "/repos/acme/web/issues")[0].Query
	for _, want := range []string{"labels=bug%2Cui", "assignee=%40me", "author=bob", "milestone=v1", "q=is%3Aclosed+crash"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q senza %q", q, want)
		}
	}
	if strings.Contains(q, "state=") {
		t.Errorf("state non esplicito non va mandato (altrimenti contraddice is: di --search): %s", q)
	}
	run(t, fk, "", "issue", "list", "--state", "all")
	if q := fk.find("GET", "/repos/acme/web/issues")[1].Query; !strings.Contains(q, "state=all") {
		t.Errorf("query %q", q)
	}
	if r := run(t, fk, "", "issue", "list", "--state", "boh"); r.code != 2 {
		t.Errorf("state errato: exit %d", r.code)
	}
}

func TestList_JSONELimiteConPagine(t *testing.T) {
	mk := func(n int) string {
		return `{"number":` + string(rune('0'+n)) + `,"title":"t","state":"open","author":` + alice + `,"labels":[],"assignees":[],"commentCount":0,"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z"}`
	}
	fk := newFake(t, map[string]reply{"GET /repos/acme/web/issues": {200, `{"items":[` + mk(1) + `,` + mk(2) + `,` + mk(3) + `],"page":1,"perPage":2,"total":3}`}})
	r := run(t, fk, "", "issue", "list", "-L", "2", "--json", "number,url")
	if r.code != 0 || strings.TrimSpace(r.out) != `[{"number":1,"url":"`+fk.srv.URL+`/acme/web/issues/1"},{"number":2,"url":"`+fk.srv.URL+`/acme/web/issues/2"}]` {
		t.Errorf("%+v", r)
	}
	if r := run(t, fk, "", "issue", "list", "--json", "boh"); r.code != 2 {
		t.Errorf("campo ignoto: exit %d", r.code)
	}
	// --json senza campi elenca i documentati
	r = run(t, fk, "", "issue", "list", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, fld := range issue.SummaryFields {
		if !strings.Contains(r.out, "  "+fld+"\n") {
			t.Errorf("campo %q non elencato: %s", fld, r.out)
		}
	}
}

func TestView_CommentiEWeb(t *testing.T) {
	comments := `{"items":[{"id":"00000000-0000-0000-0000-0000000000c1","issueNumber":7,"body":"riproduco","author":` + alice + `,"edited":false,"deleted":false,"createdAt":"2026-10-03T10:00:00Z","updatedAt":"2026-10-03T10:00:00Z"}],"page":1,"perPage":100,"total":1}`
	fk := newFake(t, map[string]reply{
		"GET /repos/acme/web/issues/7":          {200, issue7},
		"GET /repos/acme/web/issues/7/comments": {200, comments},
	})
	r := run(t, fk, "", "issue", "view", "#7")
	if r.code != 0 || !strings.Contains(r.out, "#7 Crash") || !strings.Contains(r.out, "stack trace") || strings.Contains(r.out, "riproduco") {
		t.Errorf("senza commenti: %+v", r)
	}
	r = run(t, fk, "", "issue", "view", "7", "--comments")
	if r.code != 0 || !strings.Contains(r.out, "riproduco") || !strings.Contains(r.out, "Etichette: bug") {
		t.Errorf("con commenti: %+v", r)
	}
	r = run(t, fk, "", "issue", "view", "7", "--json", "title,comments")
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got["title"] != "Crash" || len(got["comments"].([]any)) != 1 {
		t.Errorf("json: %v %+v", err, r)
	}
	r = run(t, fk, "", "issue", "view", "7", "--json", "title")
	if strings.Contains(r.out, "comments") {
		t.Errorf("comments non richiesto: %s", r.out)
	}
	var opened string
	defer issue.SetOpenURL(func(u string) error { opened = u; return nil })()
	if r = run(t, fk, "", "issue", "view", "7", "--web"); r.code != 0 || opened != fk.srv.URL+"/acme/web/issues/7" {
		t.Errorf("--web: %+v aperto %q", r, opened)
	}
	if r = run(t, fk, "", "issue", "view", "x"); r.code != 2 {
		t.Errorf("numero non valido: exit %d", r.code)
	}
	if r = run(t, fk, "", "issue", "view"); r.code != 2 {
		t.Errorf("numero mancante: exit %d", r.code)
	}
}

func TestEdit(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET /auth/session":                      {200, session},
		"GET /repos/acme/web/issues/7":           {200, issue7},
		"PATCH /repos/acme/web/issues/7":         {200, issue7},
		"PUT /repos/acme/web/issues/7/labels":    {200, issue7},
		"PUT /repos/acme/web/issues/7/assignees": {200, issue7},
		"PUT /repos/acme/web/issues/7/milestone": {200, issue7},
	})
	r := run(t, fk, "", "issue", "edit", "7", "-t", "Nuovo", "--add-label", "ui", "--remove-label", "bug", "--add-assignee", "bob", "--remove-assignee", "@me", "--remove-milestone")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if b := fk.find("PATCH", "/repos/acme/web/issues/7")[0].Body; b["title"] != "Nuovo" || b["body"] != nil {
		t.Errorf("patch: %v", b)
	}
	if b, _ := json.Marshal(fk.find("PUT", "/repos/acme/web/issues/7/labels")[0].Body); string(b) != `{"labels":["ui"]}` {
		t.Errorf("etichette: %s", b)
	}
	if b, _ := json.Marshal(fk.find("PUT", "/repos/acme/web/issues/7/assignees")[0].Body); string(b) != `{"assignees":["bob"]}` {
		t.Errorf("assegnatari: %s", b)
	}
	if b, _ := json.Marshal(fk.find("PUT", "/repos/acme/web/issues/7/milestone")[0].Body); string(b) != `{"milestone":null}` {
		t.Errorf("milestone: %s", b)
	}
	if r := run(t, fk, "", "issue", "edit", "7"); r.code != 2 {
		t.Errorf("senza modifiche: exit %d", r.code)
	}
	if r := run(t, fk, "", "issue", "edit", "7", "-m", "3", "--remove-milestone"); r.code != 2 {
		t.Errorf("flag in conflitto: exit %d", r.code)
	}
}

func TestComment(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST /repos/acme/web/issues/7/comments": {201, `{"id":"00000000-0000-0000-0000-0000000000c1","issueNumber":7,"body":"ok","author":` + alice + `,"edited":false,"deleted":false,"createdAt":"2026-10-03T10:00:00Z","updatedAt":"2026-10-03T10:00:00Z"}`}})
	r := run(t, fk, "", "issue", "comment", "7", "-b", "ok")
	if r.code != 0 || !strings.HasPrefix(r.out, fk.srv.URL+"/acme/web/issues/7#comment-00000000-") {
		t.Errorf("%+v", r)
	}
	if r = run(t, fk, "", "issue", "comment", "7"); r.code != 2 {
		t.Errorf("senza testo: exit %d", r.code)
	}
	r = run(t, fk, "", "issue", "comment", "7", "-b", "ok", "--json", "body,issueNumber")
	if strings.TrimSpace(r.out) != `{"body":"ok","issueNumber":7}` {
		t.Errorf("json: %q", r.out)
	}
}

func TestClose_Motivi(t *testing.T) {
	closed := strings.Replace(issue7, `"state":"open"`, `"state":"closed","closeReason":"completed"`, 1)
	fk := newFake(t, map[string]reply{
		"POST /repos/acme/web/issues/7/close":    {200, closed},
		"POST /repos/acme/web/issues/7/comments": {201, `{}`},
		"POST /repos/acme/web/issues/7/reopen":   {200, issue7},
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"close", "7"}, `{}`},
		{[]string{"close", "7", "-r", "completed"}, `{"reason":"completed"}`},
		{[]string{"close", "7", "-r", "not planned"}, `{"reason":"not_planned"}`},
		{[]string{"close", "7", "-r", "not_planned"}, `{"reason":"not_planned"}`},
		{[]string{"close", "7", "-r", "duplicate", "-d", "#5"}, `{"duplicateOf":5,"reason":"duplicate"}`},
		{[]string{"close", "7", "-r", "duplicate #5"}, `{"duplicateOf":5,"reason":"duplicate"}`},
		{[]string{"close", "7", "-d", "5"}, `{"duplicateOf":5,"reason":"duplicate"}`},
	} {
		before := len(fk.find("POST", "/repos/acme/web/issues/7/close"))
		r := run(t, fk, "", append([]string{"issue"}, tc.args...)...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", tc.args, r)
		}
		cs := fk.find("POST", "/repos/acme/web/issues/7/close")
		b, _ := json.Marshal(cs[before].Body)
		if string(b) != tc.want {
			t.Errorf("%v: corpo %s, voluto %s", tc.args, b, tc.want)
		}
	}
	for _, args := range [][]string{
		{"close", "7", "-r", "duplicate"},
		{"close", "7", "-r", "completed", "-d", "5"},
		{"close", "7", "-r", "boh"},
		{"close", "7", "-d", "x"},
	} {
		if r := run(t, fk, "", append([]string{"issue"}, args...)...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
	if r := run(t, fk, "", "issue", "close", "7", "-c", "fatto"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if c := fk.find("POST", "/repos/acme/web/issues/7/comments"); len(c) != 1 || c[0].Body["body"] != "fatto" {
		t.Errorf("commento: %v", c)
	}
	if r := run(t, fk, "", "issue", "reopen", "7", "--json", "state"); r.code != 0 || strings.TrimSpace(r.out) != `{"state":"open"}` {
		t.Errorf("reopen: %+v", r)
	}
}

func TestLockUnlock(t *testing.T) {
	locked := strings.Replace(issue7, `"locked":false`, `"locked":true`, 1)
	fk := newFake(t, map[string]reply{
		"PUT /repos/acme/web/issues/7/lock":    {200, locked},
		"DELETE /repos/acme/web/issues/7/lock": {200, issue7},
	})
	if r := run(t, fk, "", "issue", "lock", "7", "-r", "fuori tema", "--json", "locked"); r.code != 0 || strings.TrimSpace(r.out) != `{"locked":true}` {
		t.Errorf("lock: %+v", r)
	}
	if b := fk.find("PUT", "/repos/acme/web/issues/7/lock")[0].Body; b["reason"] != "fuori tema" {
		t.Errorf("corpo: %v", b)
	}
	if r := run(t, fk, "", "issue", "unlock", "7", "--json", "locked"); r.code != 0 || strings.TrimSpace(r.out) != `{"locked":false}` {
		t.Errorf("unlock: %+v", r)
	}
}

func TestErrori_CodiciDiUscita(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"POST /repos/acme/web/issues/1/close": {403, `{"error":{"code":"forbidden","message":"non puoi"}}`},
		"POST /repos/acme/web/issues/2/close": {409, `{"error":{"code":"archived","message":"repo archiviato"}}`},
		"POST /repos/acme/web/issues/3/close": {409, `{"error":{"code":"already_closed","message":"gia' chiusa"}}`},
		"GET /repos/acme/web/issues/5":        {200, issue7},
		"PUT /repos/acme/web/issues/6/lock":   {403, `{"error":{"code":"insufficient_scope","message":"scope"}}`},
	})
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"close", "1"}, 5, "non puoi"},
		{[]string{"close", "2"}, 1, "archiviato"},
		{[]string{"close", "3"}, 1, "gia' chiusa"},
		{[]string{"view", "99"}, 6, "non trovato"},
		{[]string{"lock", "6"}, 5, "scope"},
	}
	for _, tc := range cases {
		r := run(t, fk, "", append([]string{"issue"}, tc.args...)...)
		if r.code != tc.code || !strings.Contains(r.errOut, tc.msg) {
			t.Errorf("%v: exit %d (%q), voluto %d con %q", tc.args, r.code, r.errOut, tc.code, tc.msg)
		}
	}
	// 401: token sbagliato
	io_, _, _, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	f.Getenv = func(k string) string {
		return map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "no"}[k]
	}
	f.Git = noGit{}
	if code := root.Run(context.Background(), f, []string{"--hostname", fk.srv.URL, "-R", "acme/web", "issue", "view", "5"}); code != 4 {
		t.Errorf("401: exit %d (%s)", code, errOut)
	}
	// errore JSON su stderr con --json
	r := run(t, fk, "", "issue", "close", "1", "--json", "state")
	if r.out != "" || !strings.Contains(r.errOut, `"code":"forbidden"`) {
		t.Errorf("errore JSON: %+v", r)
	}
	// senza token locale: exit 4; senza repo: exit 2
	f.Getenv = func(k string) string { return map[string]string{config.EnvConfigDir: t.TempDir()}[k] }
	if code := root.Run(context.Background(), f, []string{"--hostname", fk.srv.URL, "-R", "acme/web", "issue", "view", "5"}); code != 4 {
		t.Errorf("senza token: exit %d", code)
	}
	f.Getenv = func(k string) string {
		return map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}[k]
	}
	if code := root.Run(context.Background(), f, []string{"--hostname", fk.srv.URL, "issue", "view", "5"}); code != 2 {
		t.Errorf("senza repo: exit %d", code)
	}
}

func TestParseCloseReason_Vuoto(t *testing.T) {
	in, err := issue.ParseCloseReason("", "")
	if err != nil || in.Reason != nil || in.DuplicateOf != nil {
		t.Errorf("%+v %v", in, err)
	}
}
