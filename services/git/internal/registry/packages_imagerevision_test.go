package registry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// readLog registra i percorsi letti con GetContent.
type readLog struct {
	storagedriver.StorageDriver
	mu    sync.Mutex
	paths []string
}

func (r *readLog) GetContent(ctx context.Context, p string) ([]byte, error) {
	r.mu.Lock()
	r.paths = append(r.paths, p)
	r.mu.Unlock()
	return r.StorageDriver.GetContent(ctx, p)
}

func (r *readLog) reset() {
	r.mu.Lock()
	r.paths = nil
	r.mu.Unlock()
}

// blobReads conta le letture di blob (manifest e config), non dei link dei tag.
func (r *readLog) blobReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.paths {
		if strings.Contains(p, "/blobs/") {
			n++
		}
	}
	return n
}

// GIT-395: ImageRevision legge solo il manifest e il config dell'immagine richiesta, per tag o
// per digest; etichetta assente o non valida dà «», immagine o tag che mancano ErrPackageNotFound.
func TestImageRevision_LookupPuntuale(t *testing.T) {
	e := newCleanEnv(t, nil)
	const commit = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"
	push := func(path string, labels map[string]string) v1.Hash {
		t.Helper()
		img, err := random.Image(1024, 1)
		if err != nil {
			t.Fatal(err)
		}
		if img, err = mutate.Config(img, v1.Config{Labels: labels}); err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(e.ref(path), img, basic("gst_write")); err != nil {
			t.Fatalf("push %s: %v", path, err)
		}
		d, err := img.Digest()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	dgOk := push("alice/app/web:sha-3f9a1c2", map[string]string{RevisionLabel: commit})
	push("alice/app/web:nolabel", nil)
	push("alice/app/web:branch", map[string]string{RevisionLabel: "main"})
	// Altre immagini e altri tag, il cui config non va letto.
	for _, p := range []string{"alice/app/web:v1", "alice/app/web:v2", "alice/app/api:v1", "alice/app:solo"} {
		push(p, map[string]string{RevisionLabel: commit})
	}
	log := &readLog{StorageDriver: e.h.driver}
	e.h.driver = log
	ctx := context.Background()

	got, err := e.h.ImageRevision(ctx, idApp, "web", "", dgOk.String())
	if err != nil || got != commit {
		t.Fatalf("per digest: %q, %v", got, err)
	}
	log.reset()
	got, err = e.h.ImageRevision(ctx, idApp, "web", "sha-3f9a1c2", "")
	if err != nil || got != commit {
		t.Fatalf("per tag: %q, %v", got, err)
	}
	// Un solo manifest e un solo config: due blob, nient'altro.
	if n := log.blobReads(); n != 2 {
		t.Fatalf("letture di blob per un tag: %d, attese 2 (%v)", n, log.paths)
	}
	for tag, desc := range map[string]string{"nolabel": "etichetta assente", "branch": "etichetta non valida"} {
		if got, err := e.h.ImageRevision(ctx, idApp, "web", tag, ""); err != nil || got != "" {
			t.Fatalf("%s: %q, %v", desc, got, err)
		}
	}
	if got, err := e.h.ImageRevision(ctx, idApp, "", "solo", ""); err != nil || got != commit {
		t.Fatalf("immagine senza nome: %q, %v", got, err)
	}
	missing := []struct{ name, image, tag, digest string }{
		{"immagine inesistente (tag)", "nope", "v1", ""},
		{"immagine inesistente (digest)", "nope", "", dgOk.String()},
		{"tag inesistente", "web", "v9", ""},
		{"digest di un'altra immagine", "api", "", dgOk.String()},
		{"tag tecnico", "web", "sha256-" + strings.Repeat("a", 64) + ".sig", ""},
	}
	for _, tc := range missing {
		if _, err := e.h.ImageRevision(ctx, idApp, tc.image, tc.tag, tc.digest); !errors.Is(err, ErrPackageNotFound) {
			t.Fatalf("%s: errore %v, atteso ErrPackageNotFound", tc.name, err)
		}
	}
	if _, err := e.h.ImageRevision(ctx, idApp, "Web!", "v1", ""); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("nome non valido: %v", err)
	}
	if _, err := e.h.ImageRevision(ctx, "non-un-uuid", "web", "v1", ""); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("repo non valido: %v", err)
	}
}
