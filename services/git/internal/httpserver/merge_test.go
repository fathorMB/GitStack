package httpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitmerge"
	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

func setupMerge(t *testing.T) (*env, string) {
	t.Helper()
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{
		Store: e.store, Content: fakeContent{}, Secret: secret,
		Reads: gitread.New(run, e.store.Dir), Merge: &gitmerge.Service{Run: run, Dir: e.store.Dir},
	})
	if _, err := e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files:  []repostore.File{{Path: "README.md", Content: []byte("# demo\n")}},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
		Now:    time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatal(err)
	}
	dir, err := e.store.Dir(rid)
	if err != nil {
		t.Fatal(err)
	}
	// Un branch con un commit in più, preparato da un clone.
	work := t.TempDir()
	g := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitrun.Env(), "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g(work, "clone", "-q", dir, "w")
	w := work + "/w"
	g(w, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(w+"/a.txt", []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g(w, "add", "-A")
	g(w, "commit", "-q", "-m", "aggiunge a")
	g(w, "push", "-q", "origin", "feature")
	return e, dir
}

func TestMergeHTTP(t *testing.T) {
	e, dir := setupMerge(t)
	base := "/internal/git/repos/" + rid

	rec := e.do("GET", base+"/compare?base=main&head=feature", "", true)
	e.expect(rec, 200)
	var cmp struct {
		AheadBy      int `json:"aheadBy"`
		FilesChanged int `json:"filesChanged"`
		Mergeability struct {
			State     string `json:"state"`
			Mergeable bool   `json:"mergeable"`
			BaseSHA   string `json:"baseSha"`
		} `json:"mergeability"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cmp); err != nil {
		t.Fatal(err)
	}
	if cmp.AheadBy != 1 || cmp.FilesChanged != 1 || cmp.Mergeability.State != "clean" || !cmp.Mergeability.Mergeable {
		t.Fatalf("confronto: %s", rec.Body)
	}
	e.expect(e.do("GET", base+"/compare?base=main&head=nessuno", "", true), 404)
	e.expect(e.do("GET", base+"/mergeability?base=main&head=feature", "", true), 200)
	e.expect(e.do("GET", base+"/compare?base=main&head=feature", "", false), 401)

	body := func(expected, method string) string {
		b, _ := json.Marshal(map[string]any{
			"base": "main", "head": "feature", "method": method, "message": "Unisce feature",
			"expectedBaseSha": expected, "author": map[string]string{"name": "Ada", "email": "ada@example.com"},
			"owner": "alice", "name": "app", "pusher": map[string]string{"id": "u-1", "username": "alice"},
		})
		return string(b)
	}
	// Base cambiata: 409 stale e nessun danno.
	rec = e.do("POST", base+"/merge", body(strings.Repeat("a", 40), "merge"), true)
	e.expect(rec, 409)
	if !strings.Contains(rec.Body.String(), `"stale"`) || !strings.Contains(rec.Body.String(), cmp.Mergeability.BaseSHA) {
		t.Fatalf("stale: %s", rec.Body)
	}
	e.expect(e.do("POST", base+"/merge", body(cmp.Mergeability.BaseSHA, "rebase"), true), 400)
	e.expect(e.do("POST", base+"/merge", "{", true), 400)

	rec = e.do("POST", base+"/merge", body(cmp.Mergeability.BaseSHA, "merge"), true)
	e.expect(rec, 200)
	var res gitmerge.MergeResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Parents) != 2 || res.Parents[0] != cmp.Mergeability.BaseSHA {
		t.Fatalf("esito: %s", rec.Body)
	}
	// Ora head è contenuto in base.
	e.expect(e.do("POST", base+"/merge", body(res.SHA, "merge"), true), 409)

	// Eliminazione: il principale no, feature sì.
	del := func(b string) *struct{ code int } {
		r := e.do("DELETE", base+"/branches", b, true)
		return &struct{ code int }{r.Code}
	}
	if c := del(`{"branch":"main"}`).code; c != 409 {
		t.Fatalf("main: %d", c)
	}
	if c := del(`{"branch":"feature"}`).code; c != 200 {
		t.Fatalf("feature: %d", c)
	}
	if c := del(`{"branch":"feature"}`).code; c != 404 {
		t.Fatalf("feature di nuovo: %d", c)
	}
	_ = dir
}
