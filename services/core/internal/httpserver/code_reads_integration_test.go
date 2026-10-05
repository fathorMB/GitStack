//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

const (
	botEmail   = "botty@agents.example.com"
	secretFile = "segreto.txt"
)

// readerGit aggiunge a fakeGit le letture del codice (gitclient.Reader) con
// risposte fisse: i test provano core (permessi, instradamento, intestazioni,
// autori), non il servizio git. Lo stato condiviso è protetto da mu (-race).
type readerGit struct {
	*fakeGit
	rmu     sync.Mutex
	streams []url.Values
	paths   []string
	reads   []readCall
}

type readCall struct {
	path string
	q    url.Values
}

const commitJSON = `{"sha":"` + "0123456789abcdef0123456789abcdef01234567" + `","subject":"s","author":{"name":"Botty","email":"Botty@Agents.Example.com","date":"2026-10-05T10:00:00Z"},"committer":{"name":"Alice","email":"alice@example.com","date":"2026-10-05T10:00:00Z"},"parents":[]}`

func (g *readerGit) ReadJSON(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	g.rmu.Lock()
	g.reads = append(g.reads, readCall{path: path, q: q})
	g.rmu.Unlock()
	switch {
	case path == "tree":
		return json.RawMessage(`{"ref":"main","commitSha":"x","path":"","entries":[{"name":"a","path":"a","type":"file","mode":"100644","size":1,"lastCommit":` + commitJSON + `}],"truncated":false}`), nil
	case path == "contents" || path == "readme":
		return json.RawMessage(`{"ref":"main","path":"a","name":"a","sha":"x","size":1,"binary":false,"kind":"text","display":"highlight","truncated":false,"content":"x","lastCommit":` + commitJSON + `}`), nil
	case path == "branches":
		return json.RawMessage(`{"items":[{"name":"main","isDefault":true,"protected":false,"commit":` + commitJSON + `},{"name":"feat/x","isDefault":false,"protected":false,"commit":` + commitJSON + `}],"total":2}`), nil
	case path == "tags":
		return json.RawMessage(`{"items":[{"name":"v1","annotated":false,"taggedAt":"2026-10-05T10:00:00Z","commit":` + commitJSON + `}],"total":1}`), nil
	case path == "commits":
		return json.RawMessage(`{"items":[` + commitJSON + `],"page":1,"perPage":30,"hasMore":false}`), nil
	case strings.HasPrefix(path, "commits/"):
		return json.RawMessage(`{"commit":` + commitJSON + `,"files":[],"filesChanged":0,"additions":0,"deletions":0,"truncated":false}`), nil
	case path == "files":
		return json.RawMessage(`{"ref":"main","commitSha":"x","paths":["a","src/b.go"],"truncated":false}`), nil
	case path == "search":
		return json.RawMessage(`{"ref":"main","query":"q","results":[{"path":"a","line":1,"fragment":"x"}],"limitReached":false,"timedOut":false}`), nil
	case path == "blame":
		return json.RawMessage(`{"ref":"main","path":"a","ranges":[{"startLine":1,"endLine":1,"commit":` + commitJSON + `}]}`), nil
	}
	return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "Percorso non trovato."}
}

func (g *readerGit) OpenStream(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (*gitclient.Stream, error) {
	g.rmu.Lock()
	g.streams = append(g.streams, q)
	g.paths = append(g.paths, path)
	g.rmu.Unlock()
	h := http.Header{}
	h.Set("Content-Type", "text/plain; charset=utf-8")
	body := "contenuto"
	switch {
	case path == "archive":
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", `attachment; filename="r-main.zip"`)
	case strings.HasPrefix(q.Get("path"), "logo.svg"):
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="logo.svg"`)
	case strings.HasSuffix(path, "/diff") || strings.HasSuffix(path, "/patch"):
		h.Set("Content-Disposition", `attachment; filename="0123456789ab.diff"`)
	case q.Get("path") == "mente.html":
		// Un git che sbagliasse tipo: core non lo inoltra come pagina.
		h.Set("Content-Type", "text/html")
	}
	return &gitclient.Stream{Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
}

var _ gitclient.Reader = (*readerGit)(nil)

// emailIdentity aggiunge a fakeIdentity il collegamento autori-utenti.
type emailIdentity struct{ *fakeIdentity }

func (emailIdentity) LookupEmails(_ context.Context, emails []string) (map[string]identityclient.CodeUser, error) {
	out := map[string]identityclient.CodeUser{}
	for _, e := range emails {
		switch e {
		case botEmail:
			out[e] = identityclient.CodeUser{ID: uuid.MustParse(carolID), Username: "botty", Kind: "agent"}
		case "alice@example.com":
			out[e] = identityclient.CodeUser{ID: uuid.MustParse(aliceID), Username: "alice", Kind: "human"}
		}
	}
	return out, nil
}

var _ identityclient.EmailLookup = emailIdentity{}

func TestCodeReads_Permessi(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	git := &readerGit{fakeGit: newFakeGit()}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}

	e.create("alice", `{"owner":"alice","name":"segreto-privato","visibility":"private"}`)
	e.create("alice", `{"owner":"alice","name":"aperto-interno","visibility":"internal"}`)

	sha := "0123456789abcdef0123456789abcdef01234567"
	reads := map[string]string{
		"tree": "/tree", "contents": "/contents?path=a", "readme": "/readme", "raw": "/raw?path=a",
		"raw_per_indirizzo": "/raw/main/a", "raw_ref_con_slash": "/raw/feat/x/dir/a.txt",
		"branches": "/branches", "tags": "/tags", "commits": "/commits", "commit": "/commits/" + sha,
		"files": "/files", "files_ref": "/files?ref=feat/x", "search": "/search?q=hello",
		"blame": "/blame?path=a", "archive_zip": "/archive?ref=main", "archive_targz": "/archive?ref=main&format=tar.gz",
		"diff": "/commits/" + sha + "/patch?format=diff", "patch": "/commits/" + sha + "/patch?format=patch",
	}
	leak := []string{"segreto-privato", "alice", "Botty"}
	for name, p := range reads {
		t.Run(name, func(t *testing.T) {
			e.want(e.do("GET", "/repos/alice/segreto-privato"+p, "alice", ""), 200)
			rec := e.do("GET", "/repos/alice/segreto-privato"+p, "bob", "")
			e.want(rec, 404)
			for _, l := range leak {
				if l == "segreto-privato" {
					continue // il nome chiesto non compare comunque nel corpo
				}
				if strings.Contains(rec.Body.String(), l) {
					t.Errorf("il 404 contiene %q: %s", l, rec.Body.String())
				}
			}
			if strings.Contains(rec.Body.String(), "segreto-privato") {
				t.Errorf("il 404 riecheggia il nome del repo: %s", rec.Body.String())
			}
			// Repo inesistente: stessa risposta.
			missing := e.do("GET", "/repos/alice/non-esiste"+p, "bob", "")
			if missing.Code != 404 || missing.Body.String() != rec.Body.String() {
				t.Errorf("404 distinguibili: %d %s / %s", missing.Code, missing.Body.String(), rec.Body.String())
			}
			// Senza identità firmata dal gateway: 401.
			req := httptest.NewRequest("GET", "/repos/alice/segreto-privato"+p, nil)
			un := httptest.NewRecorder()
			router.ServeHTTP(un, req)
			if un.Code != 401 || strings.Contains(un.Body.String(), "segreto-privato") {
				t.Errorf("senza credenziali: %d %s", un.Code, un.Body.String())
			}
			// Repo interno: leggibile da un altro utente con account (P3).
			e.want(e.do("GET", "/repos/alice/aperto-interno"+p, "bob", ""), 200)
		})
	}

	t.Run("files_e_search_instradamento", func(t *testing.T) {
		e.want(e.do("GET", "/repos/alice/aperto-interno/files", "bob", ""), 200)
		e.want(e.do("GET", "/repos/alice/aperto-interno/search?q=--help%20a.*b%26ref%3Dx&ref=feat/x", "bob", ""), 200)
		git.rmu.Lock()
		defer git.rmu.Unlock()
		var files, search url.Values
		for _, c := range git.reads {
			switch c.path {
			case "files":
				if c.q.Get("ref") == "main" {
					files = c.q
				}
			case "search":
				if c.q.Get("ref") == "feat/x" {
					search = c.q
				}
			}
		}
		if files == nil {
			t.Error("files senza ref: atteso il branch principale (main)")
		}
		if search == nil || search.Get("q") != "--help a.*b&ref=x" {
			t.Errorf("search: il testo deve arrivare intatto, ho %v", search)
		}
	})

	t.Run("ref_con_slash_e_sha", func(t *testing.T) {
		git.rmu.Lock()
		defer git.rmu.Unlock()
		found := false
		for i, q := range git.streams {
			if git.paths[i] == "raw" && q.Get("ref") == "feat/x" && q.Get("path") == "dir/a.txt" {
				found = true
			}
		}
		if !found {
			t.Errorf("raw/feat/x/dir/a.txt non risolto come ref feat/x: %v", git.streams)
		}
	})

	t.Run("raw_header_di_sicurezza", func(t *testing.T) {
		for _, p := range []string{"/raw?path=mente.html", "/raw/main/mente.html", "/raw?path=logo.svg", "/raw/main/logo.svg", "/archive?ref=main", "/commits/" + sha + "/patch"} {
			rec := e.do("GET", "/repos/alice/aperto-interno"+p, "bob", "")
			e.want(rec, 200)
			ct := strings.ToLower(rec.Header().Get("Content-Type"))
			if strings.Contains(ct, "html") || strings.Contains(ct, "svg") {
				t.Errorf("%s: Content-Type %q", p, ct)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") != "sandbox" {
				t.Errorf("%s: header di sicurezza: %v", p, rec.Header())
			}
		}
		if cd := e.do("GET", "/repos/alice/aperto-interno/raw?path=logo.svg", "bob", "").Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("svg senza attachment: %q", cd)
		}
	})

	t.Run("autore_agente_e_umano", func(t *testing.T) {
		rec := e.do("GET", "/repos/alice/aperto-interno/commits", "bob", "")
		e.want(rec, 200)
		var out struct {
			Items []struct {
				Author struct {
					User struct{ Username, Kind string }
				}
				Committer struct {
					User struct{ Username, Kind string }
				}
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != 1 {
			t.Fatalf("%v %s", err, rec.Body.String())
		}
		// L'email del commit è confrontata senza distinguere le maiuscole.
		if u := out.Items[0].Author.User; u.Username != "botty" || u.Kind != "agent" {
			t.Errorf("autore: %+v", u)
		}
		if u := out.Items[0].Committer.User; u.Kind != "human" {
			t.Errorf("committer: %+v", u)
		}
	})

	t.Run("tag_con_indirizzi_e_protezione_branch", func(t *testing.T) {
		rec := e.do("GET", "/repos/alice/aperto-interno/tags", "bob", "")
		if b := rec.Body.String(); !strings.Contains(b, `"zipUrl":"/repos/alice/aperto-interno/archive?ref=v1`) || !strings.Contains(b, "format=zip") || !strings.Contains(b, "format=tar.gz") {
			t.Errorf("tag: %s", rec.Body.String())
		}
		rec = e.do("GET", "/repos/alice/aperto-interno/branches", "bob", "")
		var out struct {
			Items []struct {
				Name      string
				Protected bool
			}
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		for _, b := range out.Items {
			if b.Protected != (b.Name == "main") { // R9: il main nasce protetto
				t.Errorf("branch %s protected=%v", b.Name, b.Protected)
			}
		}
	})

	t.Run("eliminato_come_inesistente", func(t *testing.T) {
		e.want(e.do("DELETE", "/repos/alice/aperto-interno", "alice", ""), 204)
		e.want(e.do("GET", "/repos/alice/aperto-interno/tree", "alice", ""), 404)
	})

}
