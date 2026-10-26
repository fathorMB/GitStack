package registry

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// GIT-386 (H14): l'etichetta org.opencontainers.image.revision del config blob arriva al tag
// come Revision. Senza etichetta, o con un valore che non è un commit, il campo è assente.
func TestListPackages_EtichettaRevision(t *testing.T) {
	e := newCleanEnv(t, nil)
	const commit = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"
	pushWith := func(path string, labels map[string]string) {
		t.Helper()
		img, err := random.Image(1024, 1)
		if err != nil {
			t.Fatal(err)
		}
		img, err = mutate.Config(img, v1.Config{Labels: labels})
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(e.ref(path), img, basic("gst_write")); err != nil {
			t.Fatalf("push %s: %v", path, err)
		}
	}
	pushWith("alice/app/web:sha-3f9a1c2", map[string]string{RevisionLabel: commit})
	pushWith("alice/app/web:nolabel", nil)
	pushWith("alice/app/web:branch", map[string]string{RevisionLabel: "main"})
	pushWith("alice/app/web:altro", map[string]string{"org.opencontainers.image.version": "1.2.3"})

	pk, ok := e.packages()["web"]
	if !ok {
		t.Fatal("immagine web assente")
	}
	if got := tagDetail(t, pk, "sha-3f9a1c2").Revision; got != commit {
		t.Fatalf("revision = %q, atteso %q", got, commit)
	}
	for _, tag := range []string{"nolabel", "branch", "altro"} {
		d := tagDetail(t, pk, tag)
		if d.Revision != "" {
			t.Fatalf("tag %s: revision %q, attesa assente", tag, d.Revision)
		}
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"revision"`) {
			t.Fatalf("tag %s: il campo revision deve mancare nel JSON: %s", tag, b)
		}
	}
}
