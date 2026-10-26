package httpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/registry"
)

// revPackages dà un'immagine con un tag etichettato (revision) e uno senza.
type revPackages struct{}

const revCommit = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"

func (revPackages) ListPackages(_ context.Context, _ string) ([]registry.Package, error) {
	return []registry.Package{{Image: "web", Tags: []string{"v1", "v0"}, TagDetails: []registry.PackageTag{
		{Name: "v1", Digest: "sha256:aa", Revision: revCommit},
		{Name: "v0", Digest: "sha256:bb"},
	}}}, nil
}

func (revPackages) DeletePackage(_ context.Context, _, _, _ string) error { return nil }

// GIT-386 (H14): la rotta interna dei pacchetti porta l'etichetta revision del tag, e il campo
// manca del tutto dove l'immagine non ne ha una.
func TestPackages_RevisionNellaRottaInterna(t *testing.T) {
	e, _ := newRegistryEnv(t, Deps{Packages: revPackages{}})
	rec := e.do("GET", "/internal/git/repos/"+rid+"/packages", "", true)
	e.expect(rec, 200)
	var out struct {
		Items []struct {
			TagDetails []map[string]any `json:"tagDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != 1 || len(out.Items[0].TagDetails) != 2 {
		t.Fatalf("elenco: %v %s", err, rec.Body.String())
	}
	byName := map[string]map[string]any{}
	for _, d := range out.Items[0].TagDetails {
		byName[d["name"].(string)] = d
	}
	if got, _ := byName["v1"]["revision"].(string); got != revCommit {
		t.Fatalf("tag v1: revision %q, atteso %s: %s", got, revCommit, rec.Body.String())
	}
	if _, ok := byName["v0"]["revision"]; ok {
		t.Fatalf("tag v0 senza etichetta: il campo revision deve mancare: %s", rec.Body.String())
	}
	if strings.Count(rec.Body.String(), `"revision"`) != 1 {
		t.Fatalf("revision deve comparire una sola volta: %s", rec.Body.String())
	}
}
