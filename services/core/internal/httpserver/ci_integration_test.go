//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

const ciHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ciGit serve i workflow di un repo finto: un workflow con workflow_dispatch
// in .github/workflows e uno solo su push in .gitstack/workflows.
type ciGit struct{ *readerGit }

const deployYML = "name: Deploy\non:\n  workflow_dispatch:\n    inputs:\n      env:\n        required: true\njobs:\n  d:\n    runs-on: ubuntu-latest\n"
const pushYML = "name: Build\non: push\njobs:\n  b:\n    runs-on: ubuntu-latest\n"

func (g *ciGit) ReadJSON(ctx context.Context, c trust.Identity, repo uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	notFound := &gitclient.APIError{Status: 404, Code: "not_found", Message: "Percorso non trovato."}
	entry := func(p string, size int) string {
		return `{"name":"` + p[strings.LastIndex(p, "/")+1:] + `","path":"` + p + `","type":"file","mode":"100644","size":` + "100" + `,"lastCommit":` + commitJSON + `}`
	}
	switch {
	case path == "tree" && q.Get("path") == ".github/workflows":
		return json.RawMessage(`{"ref":"x","commitSha":"x","path":".github/workflows","entries":[` + entry(".github/workflows/deploy.yml", 0) + `],"truncated":false}`), nil
	case path == "tree" && q.Get("path") == ".gitstack/workflows":
		return json.RawMessage(`{"ref":"x","commitSha":"x","path":".gitstack/workflows","entries":[` + entry(".gitstack/workflows/build.yml", 0) + `],"truncated":false}`), nil
	case path == "contents":
		body := deployYML
		if q.Get("path") == ".gitstack/workflows/build.yml" {
			body = pushYML
		}
		b, _ := json.Marshal(body)
		return json.RawMessage(`{"ref":"x","path":"` + q.Get("path") + `","name":"x","sha":"x","size":1,"binary":false,"kind":"text","display":"highlight","truncated":false,"content":` + string(b) + `,"lastCommit":` + commitJSON + `}`), nil
	case strings.HasSuffix(path, "/resolve"):
		ref := strings.TrimSuffix(strings.TrimPrefix(path, "commits/"), "/resolve")
		unescaped, _ := url.PathUnescape(ref)
		if unescaped == ciHeadSHA {
			return json.RawMessage(`{"sha":"` + ciHeadSHA + `"}`), nil
		}
		return nil, notFound
	case path == "branches":
		return json.RawMessage(`{"items":[{"name":"main","isDefault":true,"protected":false,"commit":{"sha":"` + ciHeadSHA + `"}}],"total":1}`), nil
	case path == "tags":
		return json.RawMessage(`{"items":[{"name":"v1","annotated":false,"commit":{"sha":"` + ciHeadSHA + `"}}],"total":1}`), nil
	case path == "commits" && q.Get("perPage") == "1":
		// Come il servizio git vero (gitref.Resolve): aggiunge da sé i prefissi, quindi
		// il nome breve si trova e quello completo no (GIT-451).
		if r := q.Get("ref"); r == "main" || r == "v1" {
			return json.RawMessage(`{"items":[{"sha":"` + ciHeadSHA + `","subject":"s"}],"page":1,"perPage":1}`), nil
		}
		return nil, notFound
	case path == "commits/"+ciHeadSHA:
		return json.RawMessage(`{"sha":"` + ciHeadSHA + `","subject":"s","message":"s","parents":[]}`), nil
	}
	return g.readerGit.ReadJSON(ctx, c, repo, path, q)
}

// TestCI_AvvioManualeEElenchi: GET workflows, POST dispatches (write richiesto,
// 202, input, repo archiviato) e lettura delle esecuzioni.
func TestCI_AvvioManualeEElenchi(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	git := &ciGit{&readerGit{fakeGit: newFakeGit()}}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
	e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
	base := "/repos/alice/app"

	// Elenco dei workflow: letti da entrambe le cartelle.
	rec := e.do("GET", base+"/workflows", "bob", "")
	e.want(rec, 200)
	var wl struct {
		Items []struct{ Id, Name, Path, State string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wl); err != nil || len(wl.Items) != 2 {
		t.Fatalf("workflow: %v %s", err, rec.Body.String())
	}
	paths := map[string]string{}
	for _, w := range wl.Items {
		paths[w.Path] = w.Name + "/" + w.State
	}
	if paths[".github/workflows/deploy.yml"] != "Deploy/active" || paths[".gitstack/workflows/build.yml"] != "Build/active" {
		t.Errorf("elenco: %v", paths)
	}

	// Senza write: 403 (bob legge soltanto).
	e.want(e.do("POST", base+"/workflows/deploy.yml/dispatches", "bob", `{"ref":"main","inputs":{"env":"x"}}`), 403)
	// Con write, 202 con l'esecuzione accodata.
	rec = e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"main","inputs":{"env":"prod"}}`)
	e.want(rec, 202)
	var run struct {
		Id, Status, Event, Ref, Sha, WorkflowId string
		Jobs                                    []struct{ Name, Status, RunnerType string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.Event != "workflow_dispatch" || run.Ref != "refs/heads/main" || run.Sha != ciHeadSHA ||
		run.WorkflowId != "deploy.yml" || len(run.Jobs) != 1 || run.Jobs[0].Status != "queued" || run.Jobs[0].RunnerType != "internal_pod" {
		t.Errorf("esecuzione: %s", rec.Body.String())
	}
	// GIT-451: un tag diventa refs/tags/<tag>, un ref già completo resta tale, uno sha resta uno sha.
	for in, want := range map[string]string{"v1": "refs/tags/v1", "refs/tags/v1": "refs/tags/v1", "refs/heads/main": "refs/heads/main", ciHeadSHA: ciHeadSHA} {
		rec = e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"`+in+`","inputs":{"env":"prod"}}`)
		e.want(rec, 202)
		var r struct{ Ref, Sha string }
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.Ref != want || r.Sha != ciHeadSHA {
			t.Errorf("dispatch ref %q: ref=%q sha=%q (%v)", in, r.Ref, r.Sha, err)
		}
	}
	// Errori: input mancante, workflow senza dispatch, workflow o ref inesistenti.
	e.want(e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"main"}`), 400)
	e.want(e.do("POST", base+"/workflows/build.yml/dispatches", "alice", `{"ref":"main"}`), 400)
	e.want(e.do("POST", base+"/workflows/nonc-e.yml/dispatches", "alice", `{"ref":"main"}`), 404)
	e.want(e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"nessun-branch","inputs":{"env":"x"}}`), 400)

	// Letture: elenco e dettaglio, leggibili da bob; un repo non leggibile non le espone.
	rec = e.do("GET", base+"/actions/runs", "bob", "")
	e.want(rec, 200)
	var rl struct{ Items []struct{ Id string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &rl); err != nil || len(rl.Items) != 5 { // 1 avvio + 4 di GIT-451
		t.Fatalf("elenco esecuzioni: %v %s", err, rec.Body.String())
	}
	if found := func() bool {
		for _, it := range rl.Items {
			if it.Id == run.Id {
				return true
			}
		}
		return false
	}(); !found {
		t.Fatalf("elenco esecuzioni senza il run %s: %s", run.Id, rec.Body.String())
	}
	e.want(e.do("GET", base+"/actions/runs/"+run.Id, "bob", ""), 200)
	e.want(e.do("GET", base+"/actions/runs/"+uuid.NewString(), "bob", ""), 404)

	// Repo archiviato: l'avvio manuale è 409 e l'elenco dei workflow li dà disattivati.
	if _, err := pool.Exec(context.Background(), `UPDATE core.repositories SET archived_at = now() WHERE name = 'app'`); err != nil {
		t.Fatal(err)
	}
	e.want(e.do("POST", base+"/workflows/deploy.yml/dispatches", "alice", `{"ref":"main","inputs":{"env":"x"}}`), 409)
	rec = e.do("GET", base+"/workflows", "bob", "")
	e.want(rec, 200)
	if !strings.Contains(rec.Body.String(), `"state":"disabled"`) || strings.Contains(rec.Body.String(), `"state":"active"`) {
		t.Errorf("workflow di un repo archiviato: %s", rec.Body.String())
	}
}
