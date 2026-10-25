//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// pkgGit è il servizio git finto: tiene i pacchetti e registra le eliminazioni.
type pkgGit struct {
	*fakeGit
	items     []gitclient.PackageInfo
	protected map[string]bool
	deleted   []string
	fail      error
}

func (g *pkgGit) ListPackages(_ context.Context, _ trust.Identity, _ uuid.UUID) ([]gitclient.PackageInfo, error) {
	return g.items, g.fail
}

func (g *pkgGit) DeletePackage(_ context.Context, _ trust.Identity, _ uuid.UUID, image, tag string) error {
	if g.fail != nil {
		return g.fail
	}
	found := false
	for _, p := range g.items {
		if p.Image != image {
			continue
		}
		for _, t := range p.Tags {
			if tag == "" || tag == t {
				found = true
				if g.protected[t] {
					return gitclient.ErrConflict
				}
			}
		}
	}
	if !found {
		return gitclient.ErrNotFound
	}
	g.deleted = append(g.deleted, image+":"+tag)
	return nil
}

func newPackagesEnv(t *testing.T) (*issuesEnv, *pkgGit) {
	t.Helper()
	e := newIssuesEnv(t)
	g := &pkgGit{fakeGit: newFakeGit(), protected: map[string]bool{"v1": true}}
	pushed := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	g.items = []gitclient.PackageInfo{{
		Image: "core", Tags: []string{"v1", "dev"}, Digest: "sha256:aa", SizeBytes: 1234, UpdatedAt: pushed, Signature: "verified", HasSbom: true, Kind: "image",
		PushedBy: &gitclient.PackagePusher{Kind: "job", Login: "ci job token"},
		TagDetails: []gitclient.PackageTag{
			{Name: "v1", Digest: "sha256:aa", SizeBytes: 1234, PushedAt: pushed, Signature: "verified", HasSbom: true, Protected: true, PushedBy: &gitclient.PackagePusher{Kind: "job", Login: "ci job token"}},
			{Name: "dev", Digest: "sha256:bb", SizeBytes: 1000, PushedAt: pushed.Add(-time.Hour), Signature: "unverified"},
		},
		RemovedTags: []gitclient.RemovedPackageTag{{Name: "sha-a91c3e2", Digest: "sha256:cc", Rule: "after 14 days", RemovedAt: pushed.Add(time.Hour)}},
	}, {Image: "", Tags: []string{"x"}, Signature: "unverified", UpdatedAt: pushed}}
	e.router = httpserver.NewRouter(e.pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(g),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return e, g
}

// GIT-361: GET packages (read vede, anonimo 401, repo non leggibile 404) e DELETE (solo admin).
func TestPackages_ElencoEdEliminazionePerRuolo(t *testing.T) {
	e, g := newPackagesEnv(t)
	repoID := e.repo("app", true) // internal: bob e carol leggono
	e.id.grantWrite(repoID, "bob")
	list := "/repos/alice/app/packages"

	// Anonimo (nessuna identità firmata): 401, in elenco e in eliminazione.
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, httptest.NewRequest(m, list+"?name=alice/app/core", nil))
		e.want(rec, http.StatusUnauthorized, "")
	}

	// Elenco: carol (solo read) vede; non è più 501.
	rec := e.do(http.MethodGet, list, "carol", "")
	e.want(rec, http.StatusOK, "")
	var got struct {
		Items []struct {
			Name       string `json:"name"`
			Tags       []string
			Signature  string                        `json:"signature"`
			HasSbom    bool                          `json:"hasSbom"`
			Kind       string                        `json:"kind"`
			PushedBy   *struct{ Kind, Login string } `json:"pushedBy"`
			TagDetails []struct {
				Name      string                        `json:"name"`
				Protected bool                          `json:"protected"`
				Signature string                        `json:"signature"`
				PushedBy  *struct{ Kind, Login string } `json:"pushedBy"`
			} `json:"tagDetails"`
			RemovedTags []struct {
				Name, Digest, Rule string
				RemovedAt          time.Time
			} `json:"removedTags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Items) != 2 {
		t.Fatalf("elenco: %v %s", err, rec.Body.String())
	}
	c := got.Items[0]
	if c.Name != "alice/app/core" || c.Signature != "verified" || !c.HasSbom || c.Kind != "image" || c.PushedBy == nil || c.PushedBy.Kind != "job" ||
		len(c.TagDetails) != 2 || !c.TagDetails[0].Protected || c.TagDetails[1].PushedBy != nil || c.TagDetails[1].Signature != "unverified" ||
		len(c.RemovedTags) != 1 || c.RemovedTags[0].Name != "sha-a91c3e2" || c.RemovedTags[0].Digest != "sha256:cc" || c.RemovedTags[0].Rule != "after 14 days" || c.RemovedTags[0].RemovedAt.IsZero() {
		t.Fatalf("pacchetto: %s", rec.Body.String())
	}
	if got.Items[1].Name != "alice/app" {
		t.Fatalf("immagine senza nome: %s", got.Items[1].Name)
	}
	if strings.Contains(rec.Body.String(), `"pushedBy":null`) {
		t.Fatalf("pushedBy assente deve mancare, non essere null: %s", rec.Body.String())
	}

	// Repo privato: carol non lo vede (404, mai 403).
	e.repo("riservato", false)
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/packages", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, "/repos/alice/riservato/packages?name=alice/riservato/x", "carol", ""), http.StatusNotFound, "")

	// Eliminazione: read e write → 403; solo admin (alice).
	del := list + "?name=alice/app/core&tag=dev"
	e.want(e.do(http.MethodDelete, del, "carol", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, del, "bob", ""), http.StatusForbidden, "forbidden")
	if len(g.deleted) != 0 {
		t.Fatalf("eliminato da chi non è admin: %v", g.deleted)
	}
	e.want(e.do(http.MethodDelete, del, "alice", ""), http.StatusNoContent, "")
	if len(g.deleted) != 1 || g.deleted[0] != "core:dev" {
		t.Fatalf("eliminazioni: %v", g.deleted)
	}
	// Immagine senza nome: il nome è owner/repo.
	g.items[1].Tags = []string{"x"}
	e.want(e.do(http.MethodDelete, list+"?name=alice/app", "alice", ""), http.StatusNoContent, "")
	if g.deleted[1] != ":" {
		t.Fatalf("eliminazioni: %v", g.deleted)
	}
	// Tag protetto: 409. Inesistente: 404. Nome di un altro repo: 404. Senza name: 400.
	e.want(e.do(http.MethodDelete, list+"?name=alice/app/core&tag=v1", "alice", ""), http.StatusConflict, "")
	e.want(e.do(http.MethodDelete, list+"?name=alice/app/core&tag=nope", "alice", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, list+"?name=alice/app/nope", "alice", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, list+"?name=alice/altro/core", "alice", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, list, "alice", ""), http.StatusBadRequest, "")

	// Il servizio git non risponde: 503, mai un elenco finto.
	g.fail = gitclient.ErrUnavailable
	e.want(e.do(http.MethodGet, list, "alice", ""), http.StatusServiceUnavailable, "git_unavailable")

	// Repo archiviato: si legge, non si elimina (409).
	g.fail = nil
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoID)
	e.want(e.do(http.MethodGet, list, "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, list+"?name=alice/app/core&tag=dev", "alice", ""), http.StatusConflict, "archived")
}
