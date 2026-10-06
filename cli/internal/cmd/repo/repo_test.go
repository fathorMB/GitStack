package repo_test

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

	"github.com/fathorMB/GitStack/cli/internal/cmd/repo"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type call struct {
	Method, Path, Query string
	Body                map[string]any
}

type reply struct {
	status int
	body   string
}

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

// runOpts: tty = stdin è un terminale; cfg = file hosts/<host>.yaml da scrivere.
type runOpts struct {
	stdin string
	tty   bool
	env   map[string]string
}

func runWith(t *testing.T, fk *fake, o runOpts, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(o.stdin)
	io_.InTTY = o.tty
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	for k, v := range o.env {
		env[k] = v
	}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", fk.srv.URL}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

func run(t *testing.T, fk *fake, args ...string) result {
	return runWith(t, fk, runOpts{}, args...)
}

var archivedJSON = strings.Replace(repoJSON, `"archived":false`, `"archived":true`, 1)

const (
	repoJSON = `{"id":"00000000-0000-0000-0000-0000000000c1","name":"web","owner":{"name":"acme","type":"organization"},"fullName":"acme/web",` +
		`"description":"Il sito","visibility":"private","archived":false,"defaultBranch":"main","empty":false,"protectDefaultBranch":true,` +
		`"cloneUrls":{"https":"https://git.example.com/acme/web.git","ssh":"ssh://git@git.example.com:2222/acme/web.git"},` +
		`"createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z"}`

	session      = `{"authMethod":"token","mustChangePassword":false,"user":{"username":"alice","displayName":"Alice","kind":"human"}}`
	deletedList  = `{"items":[{"id":"00000000-0000-0000-0000-0000000000d1","name":"web","owner":{"name":"acme","type":"organization"},` +
		`"deletedAt":"2026-10-03T10:00:00Z","purgeAt":"2026-10-10T10:00:00Z"}]}`
)

func TestCreate_PrivatoDiDefaultEOwnerUtente(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET /auth/session": {200, session},
		"POST /repos":       {201, strings.ReplaceAll(repoJSON, "acme", "alice")},
	})
	r := run(t, fk, "repo", "create", "web")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	b := fk.find("POST", "/repos")[0].Body
	if b["owner"] != "alice" || b["name"] != "web" || b["visibility"] != "private" {
		t.Errorf("corpo: %v", b)
	}
	for _, k := range []string{"readme", "gitignoreTemplate", "licenseTemplate", "defaultLabels"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s non va mandato senza flag: %v", k, b)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(r.out), "/alice/web") {
		t.Errorf("stdout = %q", r.out)
	}
}

func TestCreate_OrganizzazioneInternoEContenuto(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST /repos": {201, repoJSON}})
	r := run(t, fk, "repo", "create", "acme/web", "--internal", "-d", "Il sito", "--add-readme", "--gitignore", "go", "--license", "MIT", "--no-default-labels")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	if len(fk.find("GET", "/auth/session")) != 0 {
		t.Error("con owner esplicito non serve chiedere chi sono")
	}
	b := fk.find("POST", "/repos")[0].Body
	if b["owner"] != "acme" || b["visibility"] != "internal" || b["description"] != "Il sito" || b["readme"] != true ||
		b["gitignoreTemplate"] != "go" || b["licenseTemplate"] != "mit" || b["defaultLabels"] != false {
		t.Errorf("corpo: %v", b)
	}
}

func TestCreate_UsoErrato(t *testing.T) {
	fk := newFake(t, nil)
	for _, args := range [][]string{
		{"repo", "create"},
		{"repo", "create", "a/b/c"},
		{"repo", "create", "acme/web", "--owner", "altro"},
		{"repo", "create", "web", "--visibility", "public"},
		{"repo", "create", "web", "--private", "--internal"},
		{"repo", "create", "web", "--public"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2 (%s)", args, r.code, r.errOut)
		}
	}
	if fk.repoCalls() != 0 {
		t.Errorf("uso errato senza rete, ho %d chiamate", fk.repoCalls())
	}
}

func TestCreate_Json(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST /repos": {201, repoJSON}})
	r := run(t, fk, "repo", "create", "acme/web", "--json", "fullName,visibility,url")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatal(err)
	}
	if m["fullName"] != "acme/web" || m["visibility"] != "private" || !strings.HasSuffix(m["url"].(string), "/acme/web") || len(m) != 3 {
		t.Errorf("json: %v", m)
	}
}

func TestList_FiltriETabella(t *testing.T) {
	list := `{"items":[` + repoJSON + `,` + strings.NewReplacer(`"name":"web"`, `"name":"old"`, `acme/web`, `acme/old`, `"archived":false`, `"archived":true`, `"private"`, `"internal"`).Replace(repoJSON) +
		`],"page":1,"perPage":30,"total":2}`
	fk := newFake(t, map[string]reply{"GET /repos": {200, list}})
	r := run(t, fk, "repo", "list", "acme")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	if got := fk.find("GET", "/repos")[0].Query; !strings.Contains(got, "owner=acme") {
		t.Errorf("query: %s", got)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "acme/web\tprivate\tattivo") {
		t.Errorf("righe: %q", lines)
	}
	for args, want := range map[string]string{"--archived": "acme/old", "--no-archived": "acme/web", "--visibility internal": "acme/old"} {
		r := run(t, fk, append([]string{"repo", "list", "--json", "fullName", "-q", ".[].fullName"}, strings.Fields(args)...)...)
		if strings.TrimSpace(r.out) != want {
			t.Errorf("%s: %q, voluto %q (%s)", args, r.out, want, r.errOut)
		}
	}
}

func TestList_Eliminati(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /repos/deleted": {200, deletedList}})
	r := run(t, fk, "repo", "list", "--deleted", "--json", "fullName,deletedAt,purgeAt,id")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	var m []map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || len(m) != 1 || m[0]["fullName"] != "acme/web" || m[0]["purgeAt"] == nil {
		t.Errorf("json: %v %v", m, err)
	}
	if r := run(t, fk, "repo", "list", "--deleted", "--archived"); r.code != 2 {
		t.Errorf("--deleted --archived: exit %d", r.code)
	}
}

func TestView_TestoJsonEWeb(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /repos/acme/web": {200, repoJSON}})
	r := run(t, fk, "repo", "view", "acme/web")
	if r.code != 0 || !strings.Contains(r.out, "acme/web") || !strings.Contains(r.out, "Visibilità: private") ||
		!strings.Contains(r.out, "ssh://git@git.example.com:2222/acme/web.git") {
		t.Errorf("exit %d: %s | %s", r.code, r.out, r.errOut)
	}
	// Dal remote origin e con -R.
	if r := run(t, fk, "-R", "acme/web", "repo", "view", "--json", "name"); r.code != 0 || !strings.Contains(r.out, `"web"`) {
		t.Errorf("-R: exit %d: %s", r.code, r.out)
	}
	if r := run(t, fk, "repo", "view"); r.code != 2 {
		t.Errorf("senza repo: exit %d", r.code)
	}
	var opened string
	defer repo.SetOpenURL(func(u string) error { opened = u; return nil })()
	r = run(t, fk, "repo", "view", "acme/web", "--web")
	if r.code != 0 || !strings.HasSuffix(opened, "/acme/web") || !strings.HasPrefix(opened, fk.srv.URL) {
		t.Errorf("web: exit %d, aperto %q", r.code, opened)
	}
	if r := run(t, fk, "repo", "view", "acme/web", "--web", "--json", "name"); r.code != 2 {
		t.Errorf("--web --json: exit %d", r.code)
	}
	if r := run(t, fk, "repo", "view", "acme/nope"); r.code != 6 {
		t.Errorf("non trovato: exit %d", r.code)
	}
}

func TestClone_HttpsSshEConfigurazione(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /repos/acme/web": {200, repoJSON}})
	var got []string
	defer repo.SetRunGit(func(_ context.Context, _ io.Reader, _, _ io.Writer, args ...string) error { got = args; return nil })()

	if r := run(t, fk, "repo", "clone", "acme/web"); r.code != 0 || strings.Join(got, " ") != "clone https://git.example.com/acme/web.git" {
		t.Errorf("https: exit %d, git %v (%s)", r.code, got, r.errOut)
	}
	if r := run(t, fk, "repo", "clone", "acme/web", "dir", "--protocol", "ssh", "--", "--depth", "1"); r.code != 0 ||
		strings.Join(got, " ") != "clone ssh://git@git.example.com:2222/acme/web.git dir --depth 1" {
		t.Errorf("ssh: exit %d, git %v (%s)", r.code, got, r.errOut)
	}
	// git_protocol: ssh nella configurazione dell'istanza.
	dir := t.TempDir()
	cfg := config.New(dir)
	if err := cfg.SaveHost(fk.srv.URL, config.HostConfig{Host: fk.srv.URL, User: "alice", GitProtocol: "ssh"}); err != nil {
		t.Fatal(err)
	}
	if r := runWith(t, fk, runOpts{env: map[string]string{config.EnvConfigDir: dir}}, "repo", "clone", "acme/web"); r.code != 0 ||
		got[1] != "ssh://git@git.example.com:2222/acme/web.git" {
		t.Errorf("config ssh: exit %d, git %v (%s)", r.code, got, r.errOut)
	}
	if r := runWith(t, fk, runOpts{env: map[string]string{config.EnvConfigDir: dir}}, "repo", "clone", "acme/web", "-p", "https"); r.code != 0 ||
		got[1] != "https://git.example.com/acme/web.git" {
		t.Errorf("flag batte config: exit %d, git %v", r.code, got)
	}
	for _, args := range [][]string{{"repo", "clone"}, {"repo", "clone", "a/b", "c", "d"}, {"repo", "clone", "acme/web", "-p", "ftp"}} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, voluto 2", args, r.code)
		}
	}
	// Senza SSH sull'istanza.
	noSSH := strings.Replace(repoJSON, `,"ssh":"ssh://git@git.example.com:2222/acme/web.git"`, "", 1)
	fk2 := newFake(t, map[string]reply{"GET /repos/acme/web": {200, noSSH}})
	if r := run(t, fk2, "repo", "clone", "acme/web", "-p", "ssh"); r.code != 2 {
		t.Errorf("senza ssh: exit %d", r.code)
	}
}

func TestEdit(t *testing.T) {
	fk := newFake(t, map[string]reply{"PATCH /repos/acme/web": {200, repoJSON}})
	r := run(t, fk, "repo", "edit", "acme/web", "-d", "", "--visibility", "internal", "--default-branch", "develop", "--protect-default-branch=false")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	b := fk.find("PATCH", "/repos/acme/web")[0].Body
	if b["description"] != "" || b["visibility"] != "internal" || b["defaultBranch"] != "develop" || b["protectDefaultBranch"] != false {
		t.Errorf("corpo: %v", b)
	}
	if _, ok := b["archived"]; ok {
		t.Error("edit non tocca archived")
	}
	for _, args := range [][]string{{"repo", "edit", "acme/web"}, {"repo", "edit", "acme/web", "--visibility", "public"}} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d", args, r.code)
		}
	}
}

func TestArchive_ConfermeG8(t *testing.T) {
	fk := newFake(t, map[string]reply{"PATCH /repos/acme/web": {200, archivedJSON}})
	// Senza TTY e senza --yes: exit 2, niente chiamate, stdin non letto.
	if r := runWith(t, fk, runOpts{stdin: "acme/web\n"}, "repo", "archive", "acme/web"); r.code != 2 || fk.repoCalls() != 0 {
		t.Errorf("senza tty: exit %d, chiamate %d (%s)", r.code, fk.repoCalls(), r.errOut)
	}
	// TTY, nome sbagliato: annullato (exit 1), niente PATCH.
	if r := runWith(t, fk, runOpts{stdin: "web\n", tty: true}, "repo", "archive", "acme/web"); r.code != 1 || len(fk.find("PATCH", "/repos/acme/web")) != 0 {
		t.Errorf("nome sbagliato: exit %d (%s)", r.code, r.errOut)
	}
	// TTY, nome giusto: archivia.
	r := runWith(t, fk, runOpts{stdin: "acme/web\n", tty: true}, "repo", "archive", "acme/web")
	if r.code != 0 || !strings.Contains(r.errOut, "acme/web") {
		t.Fatalf("conferma: exit %d (%s)", r.code, r.errOut)
	}
	if b := fk.find("PATCH", "/repos/acme/web"); len(b) != 1 || b[0].Body["archived"] != true {
		t.Errorf("PATCH: %v", b)
	}
	// --yes senza TTY.
	if r := run(t, fk, "repo", "archive", "acme/web", "--yes"); r.code != 0 || len(fk.find("PATCH", "/repos/acme/web")) != 2 {
		t.Errorf("--yes: exit %d (%s)", r.code, r.errOut)
	}
	// unarchive non chiede conferma.
	if r := run(t, fk, "repo", "unarchive", "acme/web"); r.code != 0 {
		t.Errorf("unarchive: exit %d (%s)", r.code, r.errOut)
	} else if b := fk.find("PATCH", "/repos/acme/web"); b[2].Body["archived"] != false {
		t.Errorf("unarchive: %v", b[2].Body)
	}
}

func TestDelete_ConfermeG8(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE /repos/acme/web": {204, ``}})
	if r := run(t, fk, "repo", "delete", "acme/web"); r.code != 2 || fk.repoCalls() != 0 {
		t.Errorf("senza tty: exit %d, chiamate %d", r.code, fk.repoCalls())
	}
	if r := runWith(t, fk, runOpts{stdin: "y\n", tty: true}, "repo", "delete", "acme/web"); r.code != 1 || fk.repoCalls() != 0 {
		t.Errorf("y non basta: exit %d", r.code)
	}
	if r := runWith(t, fk, runOpts{stdin: "acme/web\n", tty: true}, "repo", "delete", "acme/web"); r.code != 0 || len(fk.find("DELETE", "/repos/acme/web")) != 1 {
		t.Errorf("conferma: exit %d (%s)", r.code, r.errOut)
	}
	if r := run(t, fk, "repo", "delete", "acme/web", "--yes"); r.code != 0 || len(fk.find("DELETE", "/repos/acme/web")) != 2 || !strings.Contains(r.errOut, "gs repo restore acme/web") {
		t.Errorf("--yes: exit %d (%s)", r.code, r.errOut)
	}
	if r := run(t, fk, "repo", "delete", "acme/nope", "-y"); r.code != 6 {
		t.Errorf("non trovato: exit %d", r.code)
	}
}

func TestRestore(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET /repos/deleted": {200, deletedList},
		"POST /repos/deleted/00000000-0000-0000-0000-0000000000d1/restore": {200, repoJSON},
	})
	r := run(t, fk, "repo", "restore", "acme/web")
	if r.code != 0 || !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web") {
		t.Fatalf("exit %d: %s | %s", r.code, r.out, r.errOut)
	}
	if q := fk.find("GET", "/repos/deleted")[0].Query; !strings.Contains(q, "owner=acme") {
		t.Errorf("query: %s", q)
	}
	if r := run(t, fk, "repo", "restore", "acme/altro"); r.code != 6 {
		t.Errorf("non eliminato: exit %d", r.code)
	}
	if r := run(t, fk, "repo", "restore"); r.code != 2 {
		t.Errorf("senza repo: exit %d", r.code)
	}
}

func TestJsonSenzaCampiElencaICampiDocumentati(t *testing.T) {
	fk := newFake(t, nil)
	r := run(t, fk, "repo", "view", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	for _, f := range repo.RepoFields {
		if !strings.Contains(r.out, "  "+f+"\n") {
			t.Errorf("manca il campo %s: %s", f, r.out)
		}
	}
}

func TestStessoCodiceDelCliReadme(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(append([]string{}, repo.RepoFields...), repo.DeletedFields...) {
		if !strings.Contains(string(b), "`"+f+"`") {
			t.Errorf("il campo %s non è documentato in cli/README.md", f)
		}
	}
}

// repoCalls conta le chiamate all'API escluso il controllo di versione di /meta.
func (fk *fake) repoCalls() int {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	n := 0
	for _, c := range fk.calls {
		if c.Path != "/meta" {
			n++
		}
	}
	return n
}
