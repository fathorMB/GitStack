//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// hexGit è il servizio git per un repo con un branch dal nome esadecimale: il
// risolutore di sha (commits/{sha}/resolve) lo prende per uno sha e non lo
// trova; solo la lettura dei commit della ref lo conosce. refs: i nomi di ref
// per cui commits?ref= risponde con la testa.
type hexGit struct {
	*ciGit
	refs map[string]bool
}

func (g *hexGit) ReadJSON(ctx context.Context, c trust.Identity, repo uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	if path == "commits" && q.Get("perPage") == "1" {
		if g.refs[q.Get("ref")] {
			return json.RawMessage(`{"items":[{"sha":"` + ciHeadSHA + `","subject":"s"}],"page":1,"perPage":1}`), nil
		}
		return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "Riferimento non trovato."}
	}
	if path == "commits/deadbeef/resolve" || path == "commits/cafebabe1/resolve" {
		return nil, &gitclient.APIError{Status: 404, Code: "not_found", Message: "Commit non trovato."}
	}
	return g.ciGit.ReadJSON(ctx, c, repo, path, q)
}

// GIT-372: un branch dal nome esadecimale (7-40 caratteri) è un branch, non uno
// sha: ListWorkflows e il dispatch lo risolvono come ref; uno sha vero resta uno sha.
func TestCI_BranchEsadecimale(t *testing.T) {
	for _, tc := range []struct {
		name string
		refs map[string]bool
	}{
		{"ref completa", map[string]bool{"refs/heads/deadbeef": true}},
		// Solo il nome nudo è noto a git: lo sha non esiste, si ripiega sulla ref.
		{"nome nudo", map[string]bool{"deadbeef": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, _ := dbtest.NewPool(t)
			id := newFakeIdentity()
			git := &hexGit{ciGit: &ciGit{&readerGit{fakeGit: newFakeGit()}}, refs: tc.refs}
			router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
				httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
				httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
			e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
			e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
			base := "/repos/alice/app"
			if _, err := pool.Exec(context.Background(), `UPDATE core.repositories SET default_branch = 'deadbeef' WHERE name = 'app'`); err != nil {
				t.Fatal(err)
			}

			// ListWorkflows parte dal branch di default, qui «deadbeef».
			rec := e.do("GET", base+"/workflows", "bob", "")
			e.want(rec, 200)
			var wl struct{ Items []struct{ Path string } }
			if err := json.Unmarshal(rec.Body.Bytes(), &wl); err != nil || len(wl.Items) != 2 {
				t.Fatalf("workflow dal branch deadbeef: %v %s", err, rec.Body.String())
			}

			// Dispatch su «deadbeef»: branch, non sha.
			rec = e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"deadbeef","inputs":{"env":"prod"}}`)
			e.want(rec, 202)
			var run struct{ Ref, Sha string }
			if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Sha != ciHeadSHA || run.Ref == "deadbeef" && tc.name == "ref completa" {
				t.Errorf("dispatch su un branch esadecimale: ref=%q sha=%q", run.Ref, run.Sha)
			}
			if tc.name == "ref completa" && run.Ref != "refs/heads/deadbeef" {
				t.Errorf("ref = %q, voluta refs/heads/deadbeef", run.Ref)
			}

			// Un ref esadecimale che non è né branch né sha resta un errore.
			e.want(e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"cafebabe1","inputs":{"env":"x"}}`), 400)

			// Uno sha vero continua a essere uno sha.
			rec = e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"`+ciHeadSHA+`","inputs":{"env":"prod"}}`)
			e.want(rec, 202)
			if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || run.Sha != ciHeadSHA || run.Ref != ciHeadSHA {
				t.Errorf("dispatch su uno sha: ref=%q sha=%q (%v)", run.Ref, run.Sha, err)
			}
		})
	}
}
