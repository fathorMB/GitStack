//go:build integration

package httpserver_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// GIT-386 (H14): un deploy di un'immagine del registry del repo scrive commit_sha dall'etichetta
// revision; senza etichetta, per un'immagine esterna o se il servizio git fallisce, resta nil e il
// deploy va avanti. La configurazione eredita il commit della versione attiva.

// commitGitPkg è il servizio git con i pacchetti finti: conta le letture di ListPackages.
type commitGitPkg struct {
	*fakeGit
	mu    sync.Mutex
	items []gitclient.PackageInfo
	err   error
	calls int
}

func (g *commitGitPkg) ListPackages(_ context.Context, _ trust.Identity, _ uuid.UUID) ([]gitclient.PackageInfo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.items, g.err
}

func (g *commitGitPkg) DeletePackage(_ context.Context, _ trust.Identity, _ uuid.UUID, _, _ string) error {
	return nil
}

// commitNoopController dà i deploy senza portarli nel cluster: il deploy resta «in corso» finché il
// test non lo chiude con finishCommitDeploy, come farebbe il controller.
type commitNoopController struct{}

func (commitNoopController) Kick()                                    {}
func (commitNoopController) Restart(context.Context, store.App) error { return nil }
func (commitNoopController) Scale(context.Context, store.App) error   { return nil }

func newCommitEnv(t *testing.T, g *commitGitPkg) *appsEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	e := &issuesEnv{t: t, pool: pool, id: id}
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	opts := []httpserver.Option{
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(g),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithAppController(commitNoopController{}),
	}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret, opts...)
	return &appsEnv{issuesEnv: e, clock: clock, git: g.fakeGit}
}

// finishCommitDeploy chiude il deploy in corso dell'app.
func (e *appsEnv) finishCommitDeploy(t *testing.T, app string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE core.app_deploys SET state = 'serving', finished_at = now()
		 WHERE app_id = (SELECT id FROM core.apps WHERE name = $1) AND state = 'in_progress'`, app); err != nil {
		t.Fatal(err)
	}
}

// commitInDB legge commit_sha della versione n dell'app.
func (e *appsEnv) commitInDB(t *testing.T, app string, n int) *string {
	t.Helper()
	var sha *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT v.commit_sha FROM core.app_versions v JOIN core.apps a ON a.id = v.app_id
		 WHERE a.name = $1 AND v.number = $2`, app, n).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	return sha
}

const (
	commitSha1    = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"
	commitSha2    = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	commitDigest1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	commitDigest2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	commitDigest3 = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func commitItems() []gitclient.PackageInfo {
	return []gitclient.PackageInfo{{Image: "web", TagDetails: []gitclient.PackageTag{
		{Name: "v1", Digest: commitDigest1, Revision: commitSha1},
		{Name: "v2", Digest: commitDigest2, Revision: commitSha2},
		{Name: "v3", Digest: commitDigest3}, // senza etichetta
	}}}
}

func TestApps_DeployScriveCommitDallEtichetta(t *testing.T) {
	g := &commitGitPkg{fakeGit: newFakeGit(), items: commitItems()}
	e := newCommitEnv(t, g)
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps"

	// Creazione da un'immagine del registry etichettata: la versione 1 porta il commit.
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:v1","port":8080}`), http.StatusAccepted, "")
	if got := e.commitInDB(t, "web", 1); got == nil || *got != commitSha1 {
		t.Fatalf("versione 1: commit_sha = %v, atteso %s", got, commitSha1)
	}
	e.finishCommitDeploy(t, "web")

	// Una modifica di configurazione eredita il commit della versione attiva (stessa immagine).
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"port":9090}`), http.StatusOK, "")
	if got := e.commitInDB(t, "web", 2); got == nil || *got != commitSha1 {
		t.Fatalf("versione di config 2: commit_sha = %v, atteso %s", got, commitSha1)
	}
	e.finishCommitDeploy(t, "web")

	// Deploy a mano di un'altra immagine etichettata: la versione 3 porta il suo commit, anche nel JSON.
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", `{"image":"git.example.com/alice/handbook/web:v2"}`), http.StatusAccepted, "")
	if got := e.commitInDB(t, "web", 3); got == nil || *got != commitSha2 {
		t.Fatalf("versione 3: commit_sha = %v, atteso %s", got, commitSha2)
	}
	l := e.versions(base+"/web/versions", "alice")
	if len(l.Items) == 0 || l.Items[0].Number != 3 || l.Items[0].CommitSha == nil || *l.Items[0].CommitSha != commitSha2 {
		t.Fatalf("API delle versioni: la più recente deve avere commitSha %s, ha %+v", commitSha2, l.Items)
	}
	e.finishCommitDeploy(t, "web")

	// Un'immagine senza etichetta: commit_sha resta nil.
	e.want(e.do(http.MethodPost, base+"/web/deploys", "alice", `{"image":"git.example.com/alice/handbook/web:v3"}`), http.StatusAccepted, "")
	if got := e.commitInDB(t, "web", 4); got != nil {
		t.Fatalf("versione 4 senza etichetta: commit_sha = %q, atteso nil", *got)
	}
}

// Un'immagine esterna non passa dal servizio git: nessuna lettura, commit nil.
func TestApps_ImmagineEsternaNonLeggeCommit(t *testing.T) {
	g := &commitGitPkg{fakeGit: newFakeGit(), items: commitItems()}
	e := newCommitEnv(t, g)
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"web","image":"ghcr.io/alice/web:v1","port":8080}`), http.StatusAccepted, "")
	if got := e.commitInDB(t, "web", 1); got != nil {
		t.Fatalf("immagine esterna: commit_sha = %q", *got)
	}
	if g.calls != 0 {
		t.Fatalf("il servizio git è stato interrogato %d volte per un'immagine esterna", g.calls)
	}
}

// Un errore del servizio git non fa fallire la creazione: il commit resta nil.
func TestApps_ErroreDelServizioGitNonBloccaIlDeploy(t *testing.T) {
	g := &commitGitPkg{fakeGit: newFakeGit(), err: gitclient.ErrNotFound}
	e := newCommitEnv(t, g)
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"web","image":"git.example.com/alice/handbook/web:v1","port":8080}`), http.StatusAccepted, "")
	if got := e.commitInDB(t, "web", 1); got != nil {
		t.Fatalf("errore del servizio git: commit_sha = %q, atteso nil", *got)
	}
}
