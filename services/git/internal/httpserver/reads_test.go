package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

func setupReads(t *testing.T) *env {
	t.Helper()
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	_, err = e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files:  []repostore.File{{Path: "README.md", Content: []byte("# demo\nriga\n")}},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
		Now:    time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReads_CommitsDetailBlameDownload(t *testing.T) {
	e := setupReads(t)
	base := "/internal/git/repos/" + rid

	rec := e.do("GET", base+"/commits?ref=main&perPage=5", "", true)
	e.expect(rec, 200)
	var list gitread.CommitList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.HasMore || list.Items[0].Subject != "Initial commit" || list.Items[0].Author.Email != "ada@example.com" {
		t.Fatalf("storico: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"user"`) {
		t.Fatalf("gli autori non portano user: %s", rec.Body)
	}
	sha := list.Items[0].SHA

	rec = e.do("GET", base+"/commits?ref=main&author=ada&path=README.md", "", true)
	e.expect(rec, 200)

	rec = e.do("GET", base+"/commits/"+sha[:8], "", true)
	e.expect(rec, 200)
	var d gitread.CommitDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.FilesChanged != 1 || d.Files[0].Path != "README.md" || d.Files[0].Status != "added" || !strings.Contains(d.Files[0].Patch, "+# demo") {
		t.Fatalf("dettaglio: %s", rec.Body)
	}

	rec = e.do("GET", base+"/blame?ref=main&path=README.md", "", true)
	e.expect(rec, 200)
	var b gitread.Blame
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Ranges) != 1 || b.Ranges[0].StartLine != 1 || b.Ranges[0].EndLine != 2 || b.Ranges[0].Commit.SHA != sha {
		t.Fatalf("blame: %s", rec.Body)
	}

	for _, f := range []string{"diff", "patch"} {
		rec = e.do("GET", base+"/commits/"+sha+"/"+f, "", true)
		e.expect(rec, 200)
		if !strings.Contains(rec.Header().Get("Content-Disposition"), `attachment; filename="`+sha[:12]+"."+f+`"`) ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Body.String(), "+# demo") {
			t.Fatalf("%s: %v %s", f, rec.Header(), rec.Body)
		}
	}
}

func TestReads_Errors(t *testing.T) {
	e := setupReads(t)
	base := "/internal/git/repos/" + rid
	cases := []struct {
		path string
		code int
		err  string
	}{
		{base + "/commits", 400, "invalid_ref"},
		{base + "/commits?ref=--all", 400, "invalid_ref"},
		{base + "/commits?ref=nonesiste", 404, "ref_not_found"},
		{base + "/commits?ref=main&path=../x", 400, "invalid_path"},
		{base + "/commits?ref=main&perPage=500", 400, "invalid_request"},
		{base + "/commits?ref=main&page=abc", 400, "invalid_request"},
		{base + "/commits/zzzzzzz", 400, "invalid_request"},
		{base + "/commits/" + strings.Repeat("a", 40), 404, "ref_not_found"},
		{base + "/commits/" + strings.Repeat("a", 40) + "/diff", 404, "ref_not_found"},
		{base + "/commits/abcdef1?ignoreWhitespace=forse", 400, "invalid_request"},
		{base + "/blame?ref=main", 400, "invalid_path"},
		{base + "/blame?ref=main&path=manca.txt", 404, "not_found"},
		{"/internal/git/repos/0a1b2c3d-1111-4222-8333-000000000000/commits?ref=main", 404, "not_found"},
		{"/internal/git/repos/non-un-uuid/commits?ref=main", 400, "invalid_request"},
	}
	for _, c := range cases {
		rec := e.do("GET", c.path, "", true)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.code || body.Error.Code != c.err {
			t.Errorf("%s: %d %s, voglio %d %s", c.path, rec.Code, rec.Body, c.code, c.err)
		}
	}
	// Senza firma, 401 anche sulle letture.
	if rec := e.do("GET", base+"/commits?ref=main", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("senza firma: %d", rec.Code)
	}
}

func TestReads_BlameTooLarge(t *testing.T) {
	e := setup(t)
	run, _ := gitrun.New()
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	_, err := e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files:  []repostore.File{{Path: "grande.txt", Content: []byte(strings.Repeat("a", gitread.MaxBlameBytes+1))}},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do("GET", "/internal/git/repos/"+rid+"/blame?ref=main&path=grande.txt", "", true)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "blame_unavailable") {
		t.Fatalf("oltre 1 MB: %d %s", rec.Code, rec.Body)
	}
}

func TestReads_Languages(t *testing.T) {
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	base := "/internal/git/repos/" + rid + "/languages"

	type langs struct {
		Languages []struct {
			Name    string  `json:"name"`
			Bytes   int64   `json:"bytes"`
			Percent float64 `json:"percent"`
		} `json:"languages"`
		TotalBytes int64 `json:"totalBytes"`
	}
	get := func(path string) langs {
		t.Helper()
		rec := e.do("GET", path, "", true)
		e.expect(rec, 200)
		var res langs
		dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&res); err != nil {
			t.Fatalf("%v: %s", err, rec.Body)
		}
		return res
	}

	// Repo creato senza contenuto: nessun commit.
	if _, err = e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
		Now:    time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatal(err)
	}
	// Repo vuoto (nessun commit): lista vuota, non 404.
	res := get(base + "?ref=main")
	if res.TotalBytes != 0 || res.Languages == nil || len(res.Languages) != 0 {
		t.Fatalf("repo vuoto: %+v", res)
	}
	rec := e.do("GET", base+"?ref=main", "", true)
	if !strings.Contains(rec.Body.String(), `"languages":[]`) {
		t.Fatalf("repo vuoto deve dare lista vuota, non null: %s", rec.Body)
	}
}

func TestReads_LanguagesContent(t *testing.T) {
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	_, err = e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files: []repostore.File{
			{Path: "README.md", Content: []byte("# demo\n")},
			{Path: "main.go", Content: []byte(strings.Repeat("a", 60))},
			{Path: "util.go", Content: []byte(strings.Repeat("b", 20))},
			{Path: "run.sh", Content: []byte(strings.Repeat("c", 20))},
			{Path: "api.gen.go", Content: []byte(strings.Repeat("e", 500))},
		},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
		Now:    time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/internal/git/repos/" + rid + "/languages"
	rec := e.do("GET", base+"?ref=main", "", true)
	e.expect(rec, 200)
	var res struct {
		Languages []struct {
			Name    string  `json:"name"`
			Bytes   int64   `json:"bytes"`
			Percent float64 `json:"percent"`
		} `json:"languages"`
		TotalBytes int64 `json:"totalBytes"`
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	if res.TotalBytes != 100 || len(res.Languages) != 2 ||
		res.Languages[0].Name != "Go" || res.Languages[0].Bytes != 80 || res.Languages[0].Percent != 80 ||
		res.Languages[1].Name != "Shell" || res.Languages[1].Bytes != 20 || res.Languages[1].Percent != 20 {
		t.Fatalf("lingue: %s", rec.Body)
	}
	// Seconda lettura (dalla cache per sha): stessa risposta.
	if rec2 := e.do("GET", base+"?ref=main", "", true); rec2.Body.String() != rec.Body.String() {
		t.Fatalf("cache: %s", rec2.Body)
	}

	for _, c := range []struct {
		path string
		code int
		err  string
	}{
		{base, 400, "invalid_ref"},
		{base + "?ref=--all", 400, "invalid_ref"},
		{base + "?ref=nonesiste", 404, "ref_not_found"},
		{"/internal/git/repos/0a1b2c3d-1111-4222-8333-000000000000/languages?ref=main", 404, "not_found"},
		{"/internal/git/repos/non-un-uuid/languages?ref=main", 400, "invalid_request"},
	} {
		rec := e.do("GET", c.path, "", true)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.code || body.Error.Code != c.err {
			t.Errorf("%s: %d %s, voglio %d %s", c.path, rec.Code, rec.Body, c.code, c.err)
		}
	}
	if rec := e.do("GET", base+"?ref=main", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("senza firma: %d", rec.Code)
	}
}

func TestReads_FilesAndSearch(t *testing.T) {
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	_, err = e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files: []repostore.File{
			{Path: "README.md", Content: []byte("# demo\n")},
			{Path: "a.go", Content: []byte("--flag e Ciao\n")},
		},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
		Now:    time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/internal/git/repos/" + rid

	rec := e.do("GET", base+"/files?ref=main", "", true)
	e.expect(rec, 200)
	var fl struct {
		Ref       string   `json:"ref"`
		CommitSHA string   `json:"commitSha"`
		Paths     []string `json:"paths"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fl); err != nil || fl.Ref != "main" || len(fl.CommitSHA) != 40 || fl.Truncated ||
		strings.Join(fl.Paths, ",") != "README.md,a.go" {
		t.Fatalf("files: %v %s", err, rec.Body)
	}

	rec = e.do("GET", base+"/search?ref=main&q=--FLAG", "", true)
	e.expect(rec, 200)
	if b := rec.Body.String(); !strings.Contains(b, `"path":"a.go"`) || !strings.Contains(b, `"line":1`) ||
		!strings.Contains(b, `"limitReached":false`) || !strings.Contains(b, `"timedOut":false`) {
		t.Fatalf("search: %s", b)
	}
	if b := e.do("GET", base+"/search?ref=main&q=nulla", "", true).Body.String(); !strings.Contains(b, `"results":[]`) {
		t.Fatalf("nessun risultato: %s", b)
	}

	for _, c := range []struct {
		path string
		code int
		err  string
	}{
		{base + "/files", 400, "invalid_ref"},
		{base + "/files?ref=nonesiste", 404, "ref_not_found"},
		{base + "/search?ref=main&q=a", 400, "invalid_request"},
		{base + "/search?ref=main", 400, "invalid_request"},
		{base + "/search?ref=--all&q=ab", 400, "invalid_ref"},
		{"/internal/git/repos/0a1b2c3d-1111-4222-8333-000000000000/search?ref=main&q=ab", 404, "not_found"},
		{"/internal/git/repos/non-un-uuid/files?ref=main", 400, "invalid_request"},
	} {
		rec := e.do("GET", c.path, "", true)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.code || body.Error.Code != c.err {
			t.Errorf("%s: %d %s, voglio %d %s", c.path, rec.Code, rec.Body, c.code, c.err)
		}
	}
	for _, p := range []string{"/files?ref=main", "/search?ref=main&q=ab"} {
		if rec := e.do("GET", base+p, "", false); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s senza firma: %d", p, rec.Code)
		}
	}
}
