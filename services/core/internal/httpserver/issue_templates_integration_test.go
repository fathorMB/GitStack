//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// M-05/H (GIT-108): modelli issue da .gitstack/ISSUE_TEMPLATE/.

// templateGit è un fake che risponde a ReadJSON con alberi e contenuti di
// modelli issue. I dati sono configurabili per ogni test.
type templateGit struct {
	*fakeGit
	rmu sync.Mutex

	// treeEntries restituisce le entries del tree per .gitstack/ISSUE_TEMPLATE/
	treeEntries func() []openapi.TreeEntry
	// treeError restituisce un errore da restituire sulle chiamate tree.
	treeError func() error
	// contents restituisce il contenuto di un file per path.
	contents func(path string) (string, bool)

	readCalls []readCall
}

func (g *templateGit) ReadJSON(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	g.rmu.Lock()
	g.readCalls = append(g.readCalls, readCall{path: path, q: q})
	g.rmu.Unlock()

	if path == "tree" && q.Get("path") == ".gitstack/ISSUE_TEMPLATE" {
		if g.treeError != nil {
			return nil, g.treeError()
		}
		entries := []openapi.TreeEntry{}
		if g.treeEntries != nil {
			entries = g.treeEntries()
		}
		ents, _ := json.Marshal(entries)
		return json.RawMessage(`{"ref":"main","commitSha":"abc","path":".gitstack/ISSUE_TEMPLATE","entries":` + string(ents) + `,"truncated":false}`), nil
	}

	if path == "contents" {
		fPath := q.Get("path")
		if g.contents != nil {
			content, ok := g.contents(fPath)
			if !ok {
				return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "Contenuto non trovato."}
			}
			sha := uuid.New().String()
			now := time.Now().UTC().Format(time.RFC3339)
			return json.RawMessage(`{"ref":"main","path":"` + fPath + `","name":"` + fPath + `","sha":"` + sha + `","size":` + fmt.Sprint(len(content)) + `,` +
				`"binary":false,"kind":"text","display":"highlight","truncated":false,"content":` + toJSON(content) + `,"lastCommit":{"sha":"abc","subject":"s","author":{"name":"Botty","email":"Botty@Agents.Example.com","date":"` + now + `"},"committer":{"name":"Alice","email":"alice@example.com","date":"` + now + `"},"parents":[]}}`), nil
		}
		return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "Contenuto non trovato."}
	}

	// Risposte default per le altre chiamate
	return json.RawMessage(`{"ref":"main","commitSha":"x","path":"","entries":[{"name":"a","path":"a","type":"file","mode":"100644","size":1,"lastCommit":{"sha":"abc","subject":"s","author":{"name":"Botty","email":"Botty@Agents.Example.com","date":"2026-10-05T10:00:00Z"},"committer":{"name":"Alice","email":"alice@example.com","date":"2026-10-05T10:00:00Z"},"parents":[]}}],"truncated":false}`), nil
}

func (g *templateGit) OpenStream(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (*gitclient.Stream, error) {
	h := http.Header{}
	h.Set("Content-Type", "text/plain; charset=utf-8")
	return &gitclient.Stream{Header: h, Body: io.NopCloser(strings.NewReader("contenuto"))}, nil
}

var _ gitclient.Reader = (*templateGit)(nil)

type templateEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	id     *fakeIdentity
	git    *templateGit
}

func newTemplateEnv(t *testing.T) *templateEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	git := &templateGit{fakeGit: newFakeGit()}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return &templateEnv{t: t, pool: pool, router: router, id: id, git: git}
}

func (e *templateEnv) do(method, path, user, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *templateEnv) want(rec *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("risposta %d %s, voluto %d", rec.Code, rec.Body.String(), status)
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"`+code+`"`) {
		e.t.Fatalf("risposta %s, voluto il codice %q", rec.Body.String(), code)
	}
}

func (e *templateEnv) createRepo(name, vis string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos", "alice", fmt.Sprintf(`{"owner":"alice","name":%q,"visibility":%q}`, name, vis))
	e.want(rec, http.StatusCreated, "")
}

// toJSON produce un JSON string valido da inserire in un JSON più grande.
func toJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func itemNames(items []openapi.IssueTemplate) []string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}

func decodeTemplates(rec *httptest.ResponseRecorder) openapi.IssueTemplateList {
	var out openapi.IssueTemplateList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		panic(err)
	}
	return out
}

func TestIssueTemplates_SenzaCartella(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-test", "internal")
	// Il tree risponde con 404 not_found (cartella assente su git).
	e.git.treeError = func() error {
		return &gitclient.APIError{Status: 404, Code: "not_found", Message: "Percorso non trovato."}
	}
	rec := e.do("GET", "/repos/alice/template-test/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 0 {
		t.Fatalf("atteso 0 modelli, trovato %d", len(out.Items))
	}
	if out.Items == nil {
		t.Errorf("items = nil, voluto []")
	}
}

func TestIssueTemplates_UnModello(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-one", "internal")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "bug.md", Path: ".gitstack/ISSUE_TEMPLATE/bug.md", Type: "file", Mode: "100644"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		if path == ".gitstack/ISSUE_TEMPLATE/bug.md" {
			return "---\ntitle: Bug Report\nabout: Segnala un bug\nlabels:\n  - bug\n  - triage\n---\n\n## Steps to reproduce\n\n...\n", true
		}
		return "", false
	}

	rec := e.do("GET", "/repos/alice/template-one/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 1 {
		t.Fatalf("atteso 1 modello, trovato %d", len(out.Items))
	}
	if out.Items[0].Name != "bug" {
		t.Errorf("name = %q, voluto %q", out.Items[0].Name, "bug")
	}
	if out.Items[0].Title == nil || *out.Items[0].Title != "Bug Report" {
		t.Errorf("title = %v, voluto %q", out.Items[0].Title, "Bug Report")
	}
	if out.Items[0].About == nil || *out.Items[0].About != "Segnala un bug" {
		t.Errorf("about = %v, voluto %q", out.Items[0].About, "Segnala un bug")
	}
	labels := *out.Items[0].Labels
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "triage" {
		t.Errorf("labels = %v, voluto [bug triage]", labels)
	}
	if !strings.Contains(out.Items[0].Body, "Steps to reproduce") {
		t.Errorf("body non contiene il contenuto markdown: %s", out.Items[0].Body)
	}
}

func TestIssueTemplates_PiuModelliOrdinati(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-multi", "internal")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "feature.md", Path: ".gitstack/ISSUE_TEMPLATE/feature.md", Type: "file", Mode: "100644"},
			{Name: "bug.md", Path: ".gitstack/ISSUE_TEMPLATE/bug.md", Type: "file", Mode: "100644"},
			{Name: "docs.md", Path: ".gitstack/ISSUE_TEMPLATE/docs.md", Type: "file", Mode: "100644"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		return "---\ntitle: Template\n---\n\nTest body\n", true
	}

	rec := e.do("GET", "/repos/alice/template-multi/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 3 {
		t.Fatalf("atteso 3 modelli, trovato %d", len(out.Items))
	}
	if out.Items[0].Name != "bug" || out.Items[1].Name != "docs" || out.Items[2].Name != "feature" {
		t.Errorf("ordine = %v, voluto [bug docs feature]", itemNames(out.Items))
	}
}

func TestIssueTemplates_ModelloMalformatoSaltato(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-bad", "internal")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "good.md", Path: ".gitstack/ISSUE_TEMPLATE/good.md", Type: "file", Mode: "100644"},
			{Name: "bad.md", Path: ".gitstack/ISSUE_TEMPLATE/bad.md", Type: "file", Mode: "100644"},
			{Name: "also_good.md", Path: ".gitstack/ISSUE_TEMPLATE/also_good.md", Type: "file", Mode: "100644"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		switch path {
		case ".gitstack/ISSUE_TEMPLATE/good.md":
			return "---\ntitle: Good\n---\n\nTest\n", true
		case ".gitstack/ISSUE_TEMPLATE/bad.md":
			return "---\ntitle: [INVALID YAML {{{\n", true
		case ".gitstack/ISSUE_TEMPLATE/also_good.md":
			return "---\ntitle: Also Good\nabout: Another\nlabels:\n  - feature\n---\n\nAltro\n", true
		}
		return "", false
	}

	rec := e.do("GET", "/repos/alice/template-bad/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 2 {
		t.Fatalf("atteso 2 modelli (bad saltato), trovato %d: %v", len(out.Items), itemNames(out.Items))
	}
	if out.Items[0].Name != "also_good" || out.Items[1].Name != "good" {
		t.Errorf("nomi = %v, voluto [also_good good]", itemNames(out.Items))
	}
}

func TestIssueTemplates_ModelloSenzaFrontMatter(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-no-fm", "internal")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "blank.md", Path: ".gitstack/ISSUE_TEMPLATE/blank.md", Type: "file", Mode: "100644"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		return "## Descrizione\n\nInserisci qui...\n", true
	}

	rec := e.do("GET", "/repos/alice/template-no-fm/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 1 {
		t.Fatalf("atteso 1 modello, trovato %d", len(out.Items))
	}
	if out.Items[0].Name != "blank" {
		t.Errorf("name = %q, voluto %q", out.Items[0].Name, "blank")
	}
	if out.Items[0].Title != nil {
		t.Errorf("title = %v, voluto nil", out.Items[0].Title)
	}
	if out.Items[0].Labels != nil {
		t.Errorf("labels = %v, voluto nil", *out.Items[0].Labels)
	}
	if !strings.Contains(out.Items[0].Body, "Descrizione") {
		t.Errorf("body = %q, voluto contenuto intero", out.Items[0].Body)
	}
}

func TestIssueTemplates_SoloMarkdownNonSaltati(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-md-only", "internal")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "template.md", Path: ".gitstack/ISSUE_TEMPLATE/template.md", Type: "file", Mode: "100644"},
			{Name: "readme.txt", Path: ".gitstack/ISSUE_TEMPLATE/README.txt", Type: "file", Mode: "100644"},
			{Name: "notes", Path: ".gitstack/ISSUE_TEMPLATE/notes", Type: "file", Mode: "100644"},
			{Name: "subdir", Path: ".gitstack/ISSUE_TEMPLATE/subdir", Type: "dir", Mode: "40000"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		return "---\ntitle: OnlyMD\n---\n\nOK\n", true
	}

	rec := e.do("GET", "/repos/alice/template-md-only/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 1 {
		t.Fatalf("atteso 1 modello (solo .md), trovato %d", len(out.Items))
	}
	if out.Items[0].Name != "template" {
		t.Errorf("name = %q, voluto %q", out.Items[0].Name, "template")
	}
}

func TestIssueTemplates_Permessi(t *testing.T) {
	e := newTemplateEnv(t)
	// alice crea un repo privato
	e.createRepo("template-private", "private")
	e.git.treeEntries = func() []openapi.TreeEntry {
		return []openapi.TreeEntry{
			{Name: "bug.md", Path: ".gitstack/ISSUE_TEMPLATE/bug.md", Type: "file", Mode: "100644"},
		}
	}
	e.git.contents = func(path string) (string, bool) {
		return "---\ntitle: Bug\n---\n\nTest\n", true
	}

	// alice può leggere (è owner)
	rec := e.do("GET", "/repos/alice/template-private/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")

	// bob non può leggere (repo privato, bob è solo user)
	rec = e.do("GET", "/repos/alice/template-private/issue-templates", "bob", "")
	e.want(rec, http.StatusNotFound, "not_found")
}

func TestIssueTemplates_RefNotFound(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-empty", "internal")
	// Il tree risponde con 404 ref_not_found (repo vuoto).
	e.git.treeError = func() error {
		return &gitclient.APIError{Status: 404, Code: "ref_not_found", Message: "Ref non trovato."}
	}
	rec := e.do("GET", "/repos/alice/template-empty/issue-templates", "alice", "")
	e.want(rec, http.StatusOK, "")
	out := decodeTemplates(rec)
	if len(out.Items) != 0 {
		t.Fatalf("atteso 0 modelli, trovato %d", len(out.Items))
	}
}

func TestIssueTemplates_Git503(t *testing.T) {
	e := newTemplateEnv(t)
	e.createRepo("template-503", "internal")
	// Il tree risponde con 503 (git non disponibile).
	e.git.treeError = func() error {
		return &gitclient.APIError{Status: 503, Code: "git_unavailable", Message: "Servizio git non disponibile."}
	}
	rec := e.do("GET", "/repos/alice/template-503/issue-templates", "alice", "")
	e.want(rec, http.StatusServiceUnavailable, "git_unavailable")
}
