//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

type releaseGit struct{ *fakeGit }

func (g *releaseGit) ReadJSON(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, _ url.Values) (json.RawMessage, error) {
	switch path {
	case "tags":
		return json.RawMessage(`{"items":[{"name":"v0.1.0","taggedAt":"2026-10-01T00:00:00Z"},{"name":"v1.0.0","taggedAt":"2099-01-01T00:00:00Z"},{"name":"v1.1.0","taggedAt":"2099-02-01T00:00:00Z"}],"total":3}`), nil
	case "compare":
		return json.RawMessage(`{"commits":[{"sha":"0123456789abcdef","subject":"Corregge il rilascio"}]}`), nil
	default:
		return nil, gitclient.ErrNotFound
	}
}

func (g *releaseGit) OpenStream(context.Context, trust.Identity, uuid.UUID, string, url.Values) (*gitclient.Stream, error) {
	return nil, gitclient.ErrUnavailable
}

type releaseEnv struct {
	*reposEnv
	assets string
}

func newReleaseEnv(t *testing.T) *releaseEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	assets := t.TempDir()
	id := newFakeIdentity()
	git := &releaseGit{fakeGit: newFakeGit()}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithAttachments(httpserver.AttachmentsConfig{ReleaseAssets: &attachments.Disk{Dir: assets}}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
	e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
	return &releaseEnv{reposEnv: e, assets: assets}
}

func (e *releaseEnv) release(method, path, user, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(method, path, user, body)
}

func (e *releaseEnv) upload(id uuid.UUID, name string, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/repos/alice/app/releases/"+id.String()+"/assets?name="+url.QueryEscape(name), bytes.NewReader(data))
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users["alice"], Username: "alice"}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func TestReleases_API_NoteGenerateEAsset(t *testing.T) {
	e := newReleaseEnv(t)

	// La prima release pubblicata e' il limite inferiore del confronto Git.
	e.want(e.release(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v0.1.0","name":"Prima"}`), http.StatusCreated)

	var repoID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT resource_id FROM core.repositories WHERE name='app'`).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.pull_requests (id,repo_id,number,source_branch,target_branch,title,state,author_id,merged_at) VALUES ($1,$2,1,'release/fix','main','Corregge il rilascio','merged',$3,now())`, uuid.New(), repoID, uuid.MustParse(aliceID)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.pull_requests (id,repo_id,number,source_branch,target_branch,title,state,author_id,merged_at) VALUES ($1,$2,2,'old/fix','main','PR precedente','merged',$3,'2026-09-01T00:00:00Z')`, uuid.New(), repoID, uuid.MustParse(aliceID)); err != nil {
		t.Fatal(err)
	}

	created := e.release(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v1.0.0","name":"Versione 1","draft":true,"generateNotes":true}`)
	e.want(created, http.StatusCreated)
	var rel openapi.Release
	if err := json.Unmarshal(created.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}
	if !rel.Draft || rel.Latest || !strings.Contains(rel.Body, "Corregge il rilascio (`0123456`)") || !strings.Contains(rel.Body, "#1 Corregge il rilascio") || strings.Contains(rel.Body, "PR precedente") {
		t.Fatalf("release generata inattesa: %+v", rel)
	}

	assetBytes := []byte("archivio della release\n")
	up := e.upload(uuid.UUID(rel.Id), "build.zip", assetBytes)
	e.want(up, http.StatusCreated)
	var asset openapi.ReleaseAsset
	if err := json.Unmarshal(up.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.Sha256 != "bd980bbf27cb7a87f212b8e83ce3e52c37c6b926a096e2755e30719a0b768c45" {
		t.Fatalf("digest = %s", asset.Sha256)
	}

	dlPath := "/repos/alice/app/releases/" + rel.Id.String() + "/assets/" + asset.Id.String() + "/download"
	// Una bozza non e' visibile a chi ha solo read.
	e.want(e.release(http.MethodGet, dlPath, "bob", ""), http.StatusNotFound)
	e.want(e.release(http.MethodGet, "/repos/alice/app/releases/"+rel.Id.String(), "bob", ""), http.StatusNotFound)
	bobList := e.release(http.MethodGet, "/repos/alice/app/releases", "bob", "")
	e.want(bobList, http.StatusOK)
	var bl openapi.ReleaseList
	if err := json.Unmarshal(bobList.Body.Bytes(), &bl); err != nil || len(bl.Items) != 1 {
		t.Fatalf("bob non deve vedere la bozza: %v (%s)", err, bobList.Body.String())
	}
	aliceList := e.release(http.MethodGet, "/repos/alice/app/releases", "alice", "")
	if err := json.Unmarshal(aliceList.Body.Bytes(), &bl); err != nil || len(bl.Items) != 2 {
		t.Fatalf("alice deve vedere la bozza: %v (%s)", err, aliceList.Body.String())
	}
	e.want(e.release(http.MethodGet, "/repos/alice/app/releases/"+rel.Id.String(), "alice", ""), http.StatusOK)
	download := e.release(http.MethodGet, dlPath, "alice", "")
	e.want(download, http.StatusOK)
	if !bytes.Equal(download.Body.Bytes(), assetBytes) || download.Header().Get("Content-Length") != "23" {
		t.Fatalf("download inatteso: body=%q headers=%v", download.Body.Bytes(), download.Header())
	}

	updated := e.release(http.MethodPatch, "/repos/alice/app/releases/"+rel.Id.String(), "alice", `{"draft":false,"prerelease":true}`)
	e.want(updated, http.StatusOK)
	var published openapi.Release
	if err := json.Unmarshal(updated.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Draft || published.Latest || published.PublishedAt == nil {
		t.Fatalf("stato pre-release inatteso: %+v", published)
	}

	list := e.release(http.MethodGet, "/repos/alice/app/releases", "bob", "")
	e.want(list, http.StatusOK)
	var all openapi.ReleaseList
	if err := json.Unmarshal(list.Body.Bytes(), &all); err != nil || len(all.Items) != 2 {
		t.Fatalf("lista release: %v (%s)", err, list.Body.String())
	}

	latestOf := func(tag string) bool {
		t.Helper()
		l := e.release(http.MethodGet, "/repos/alice/app/releases", "bob", "")
		var ll openapi.ReleaseList
		if err := json.Unmarshal(l.Body.Bytes(), &ll); err != nil {
			t.Fatal(err)
		}
		for _, x := range ll.Items {
			if x.TagName == tag {
				return x.Latest
			}
		}
		t.Fatalf("release %s assente", tag)
		return false
	}
	if !latestOf("v0.1.0") || latestOf("v1.0.0") {
		t.Fatalf("Latest atteso su v0.1.0 e non sulla pre-release: %s", list.Body.String())
	}
	e.want(e.release(http.MethodGet, dlPath, "bob", ""), http.StatusOK)
	time.Sleep(20 * time.Millisecond)
	e.want(e.release(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v1.1.0","name":"Terza"}`), http.StatusCreated)
	if latestOf("v0.1.0") || !latestOf("v1.1.0") || latestOf("v1.0.0") {
		t.Fatal("Latest deve passare alla terza release stabile")
	}

	missing := e.release(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v9.9.9","name":"No"}`)
	e.want(missing, http.StatusUnprocessableEntity)
}

func TestReleases_WebhookFirmato(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", true)
	e.repoHook("app", `{"url":"http://hooks.example.test/in","events":["release"],"secret":"release-secret"}`)

	created := e.do(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v1.0.0","name":"Versione 1"}`)
	e.want(created, http.StatusCreated, "")
	e.run()
	reqs := e.recv.requests()
	if len(reqs) != 1 || reqs[0].header.Get("X-GitStack-Event") != "release" {
		t.Fatalf("webhook release: %+v", reqs)
	}
	verifySignature(t, "release-secret", reqs[0])
}

func TestReleases_WebhookAzioni(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", true)
	e.repoHook("app", `{"url":"http://hooks.example.test/in","events":["release"],"secret":"release-secret"}`)

	created := e.do(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v1.0.0","name":"Versione 1","draft":true}`)
	e.want(created, http.StatusCreated, "")
	var rel openapi.Release
	if err := json.Unmarshal(created.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}
	path := "/repos/alice/app/releases/" + rel.Id.String()
	e.want(e.do(http.MethodPatch, path, "alice", `{"draft":false}`), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, path, "alice", `{"name":"Rinominata"}`), http.StatusOK, "")

	rows, err := e.pool.Query(context.Background(), `SELECT action FROM core.webhook_deliveries WHERE event='release' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		counts[a]++
	}
	if counts["created"] != 1 || counts["published"] != 1 || counts["edited"] != 1 {
		t.Fatalf("azioni webhook inattese: %v", counts)
	}
}
