package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// GIT-386 (H14): il commit di un deploy viene dall'etichetta revision letta dal servizio git per
// le immagini del registry del repo. Niente di esterno, niente di non letto, nessun errore che
// fermi il deploy.

const (
	commitTestSecret = "segreto-di-prova"
	commitTestSha    = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"
)

var commitTestDigest = "sha256:" + strings.Repeat("d", 64)

// commitGit è il servizio git finto: il lookup puntuale ImageRevision conta e cerca in items come
// la rotta vera; ListPackages fa fallire il test, perché il commit non deve più elencare il repo.
type commitGit struct {
	gitclient.Git
	t     *testing.T
	items []gitclient.PackageInfo
	err   error
	calls int
}

func (g *commitGit) ListPackages(_ context.Context, _ trust.Identity, _ uuid.UUID) ([]gitclient.PackageInfo, error) {
	g.t.Helper()
	g.t.Error("imageCommit ha chiamato ListPackages: serve il lookup puntuale ImageRevision")
	return nil, nil
}

func (g *commitGit) ImageRevision(_ context.Context, _ trust.Identity, _ uuid.UUID, image, tag, digest string) (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	for _, p := range g.items {
		if p.Image != image {
			continue
		}
		for _, t := range p.TagDetails {
			if (digest != "" && t.Digest == digest) || (digest == "" && t.Name == tag) {
				return t.Revision, nil
			}
		}
	}
	return "", gitclient.ErrNotFound
}

func (g *commitGit) DeletePackage(_ context.Context, _ trust.Identity, _ uuid.UUID, _, _ string) error {
	return nil
}

// commitCtx dà un contesto con un'identità firmata, come quello di una richiesta che è passata da
// trust.Require: è il solo modo di farla arrivare a imageCommit.
func commitCtx(t *testing.T) context.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	trust.Sign(req.Header, commitTestSecret, trust.Identity{UserID: uuid.NewString(), Username: "alice"}, time.Now())
	var ctx context.Context
	h := trust.Require(commitTestSecret, nil, nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if ctx == nil {
		t.Fatal("identità non accettata dal middleware di prova")
	}
	return ctx
}

func commitServer(g gitclient.Git) *apiServer {
	return &apiServer{git: g, clone: CloneConfig{PublicURL: "https://git.example.com"}}
}

func commitRepo() store.Repo {
	return store.Repo{ID: uuid.New(), OwnerName: "Alice", Name: "Handbook"}
}

func TestImageCommit_EtichettaDelRegistry(t *testing.T) {
	ref := "git.example.com/alice/handbook/web:sha-3f9a1c2"
	withRev := []gitclient.PackageInfo{{Image: "web", TagDetails: []gitclient.PackageTag{
		{Name: "sha-3f9a1c2", Digest: commitTestDigest, Revision: commitTestSha},
		{Name: "sha-0000000", Digest: "sha256:" + strings.Repeat("e", 64)},
	}}}
	cases := []struct {
		name  string
		img   store.AppImage
		items []gitclient.PackageInfo
		want  string // «» = nil
	}{
		{
			name:  "tag con etichetta: il commit del tag",
			img:   store.AppImage{Reference: ref, Source: "registry"},
			items: withRev,
			want:  commitTestSha,
		},
		{
			name:  "digest nel riferimento: vale il digest, non il tag",
			img:   store.AppImage{Reference: "git.example.com/alice/handbook/web@" + commitTestDigest, Digest: ptrCommit(commitTestDigest), Source: "registry"},
			items: withRev,
			want:  commitTestSha,
		},
		{
			name:  "tag senza etichetta: il campo resta assente",
			img:   store.AppImage{Reference: "git.example.com/alice/handbook/web:sha-0000000", Source: "registry"},
			items: withRev,
		},
		{
			name:  "tag che non c'è nel registry: assente",
			img:   store.AppImage{Reference: "git.example.com/alice/handbook/web:v9", Source: "registry"},
			items: withRev,
		},
		{
			name:  "immagine senza nome (solo tag del repo)",
			img:   store.AppImage{Reference: "git.example.com/alice/handbook:v2", Source: "registry"},
			items: []gitclient.PackageInfo{{Image: "", TagDetails: []gitclient.PackageTag{{Name: "v2", Digest: commitTestDigest, Revision: commitTestSha}}}},
			want:  commitTestSha,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &commitGit{t: t, items: tc.items}
			got := commitServer(g).imageCommit(commitCtx(t), commitRepo(), tc.img)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("commit %q, atteso assente", *got)
				}
				return
			}
			if got == nil || *got != tc.want {
				t.Fatalf("commit = %v, atteso %q", got, tc.want)
			}
		})
	}
}

// Un'immagine esterna non passa mai dal registry del repo: nessuna chiamata al servizio git.
func TestImageCommit_EsternaNonLegge(t *testing.T) {
	g := &commitGit{t: t, items: []gitclient.PackageInfo{{Image: "web", TagDetails: []gitclient.PackageTag{{Name: "v1", Revision: commitTestSha}}}}}
	img := store.AppImage{Reference: "ghcr.io/alice/web:v1", Source: "external"}
	if got := commitServer(g).imageCommit(commitCtx(t), commitRepo(), img); got != nil {
		t.Fatalf("immagine esterna con commit %q", *got)
	}
	if g.calls != 0 {
		t.Fatalf("il servizio git è stato interrogato %d volte per un'immagine esterna", g.calls)
	}
}

// Un errore del servizio git non fa fallire il deploy: il commit resta nil (e si scrive un log).
func TestImageCommit_ErroreDelServizioGitNonBloccaIlDeploy(t *testing.T) {
	g := &commitGit{t: t, err: gitclient.ErrNotFound}
	img := store.AppImage{Reference: "git.example.com/alice/handbook/web:v1", Source: "registry"}
	if got := commitServer(g).imageCommit(commitCtx(t), commitRepo(), img); got != nil {
		t.Fatalf("commit %q con errore del servizio git", *got)
	}
	if g.calls != 1 {
		t.Fatalf("chiamate al servizio git: %d, attesa 1", g.calls)
	}
}

// Un errore generico o un timeout (non ErrNotFound) segue il ramo con warning: il deploy va avanti,
// commit nil, una sola lettura.
func TestImageCommit_ErroreGenericoETimeoutNonBloccanoIlDeploy(t *testing.T) {
	for name, err := range map[string]error{
		"errore generico": errors.New("git giù"),
		"timeout":         context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			g := &commitGit{t: t, err: err}
			img := store.AppImage{Reference: "git.example.com/alice/handbook/web:v1", Source: "registry"}
			if got := commitServer(g).imageCommit(commitCtx(t), commitRepo(), img); got != nil {
				t.Fatalf("commit %q con errore del servizio git", *got)
			}
			if g.calls != 1 {
				t.Fatalf("chiamate al servizio git: %d, attesa 1", g.calls)
			}
		})
	}
}

// Senza un servizio git configurato il commit è semplicemente assente.
func TestImageCommit_SenzaServizioGit(t *testing.T) {
	img := store.AppImage{Reference: "git.example.com/alice/handbook/web:v1", Source: "registry"}
	if got := commitServer(nil).imageCommit(commitCtx(t), commitRepo(), img); got != nil {
		t.Fatalf("commit %q senza servizio git", *got)
	}
}

func TestRegistryParts(t *testing.T) {
	s := commitServer(nil)
	repo := commitRepo()
	cases := []struct {
		ref, image, tag, digest string
	}{
		{"git.example.com/alice/handbook:v2", "", "v2", ""},
		{"git.example.com/alice/handbook/web:sha-1", "web", "sha-1", ""},
		{"git.example.com/alice/handbook/web@" + commitTestDigest, "web", "", commitTestDigest},
		{"git.example.com/alice/handbook/api/worker:v3@" + commitTestDigest, "api/worker", "v3", commitTestDigest},
		{"ghcr.io/alice/web:v1", "", "", ""},
	}
	for _, tc := range cases {
		image, tag, digest := s.registryParts(tc.ref, repo)
		if image != tc.image || tag != tc.tag || digest != tc.digest {
			t.Errorf("registryParts(%q) = (%q, %q, %q), atteso (%q, %q, %q)", tc.ref, image, tag, digest, tc.image, tc.tag, tc.digest)
		}
	}
}

func ptrCommit(s string) *string { return &s }
