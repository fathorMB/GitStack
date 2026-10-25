package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/registry"
)

type fakePackages struct {
	listErr, delErr error
	gotRepo         string
	gotImage        string
	gotTag          string
}

func (f *fakePackages) ListPackages(_ context.Context, repoID string) ([]registry.Package, error) {
	f.gotRepo = repoID
	if f.listErr != nil {
		return nil, f.listErr
	}
	return []registry.Package{{Image: "core", Tags: []string{"v1"}, Digest: "sha256:aa", UpdatedAt: time.Unix(1, 0).UTC(),
		Signature: registry.SignatureVerified, Kind: "image",
		TagDetails: []registry.PackageTag{{Name: "v1", Digest: "sha256:aa", Signature: registry.SignatureVerified, PushedBy: &registry.Pusher{Kind: "job", Login: "ci job token"}}}}}, nil
}

func (f *fakePackages) DeletePackage(_ context.Context, repoID, image, tag string) error {
	f.gotRepo, f.gotImage, f.gotTag = repoID, image, tag
	return f.delErr
}

// GIT-361: l'API interna dei pacchetti vuole la firma di servizio e mappa gli errori del registry.
func TestPackages_RotteInterne(t *testing.T) {
	f := &fakePackages{}
	e, _ := newRegistryEnv(t, Deps{Packages: f})
	path := "/internal/git/repos/" + rid + "/packages"

	e.expect(e.do("GET", path, "", false), 401)
	e.expect(e.do("DELETE", path+"?image=core", "", false), 401)

	rec := e.do("GET", path, "", true)
	e.expect(rec, 200)
	var out struct {
		Items []struct {
			Image      string `json:"image"`
			Signature  string `json:"signature"`
			TagDetails []struct {
				PushedBy struct{ Kind, Login string } `json:"pushedBy"`
			} `json:"tagDetails"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Items) != 1 || out.Items[0].Image != "core" ||
		out.Items[0].Signature != "verified" || out.Items[0].TagDetails[0].PushedBy.Kind != "job" || f.gotRepo != rid {
		t.Fatalf("elenco: %v %s", err, rec.Body.String())
	}

	e.expect(e.do("DELETE", path+"?image="+url.QueryEscape("a/b")+"&tag=v1", "", true), 204)
	if f.gotImage != "a/b" || f.gotTag != "v1" || f.gotRepo != rid {
		t.Fatalf("delete: %+v", f)
	}
	for err, code := range map[error]int{
		registry.ErrPackageNotFound: 404, registry.ErrProtectedTag: 409, registry.ErrInvalidImage: 400, errors.New("disco"): 500,
	} {
		f.delErr, f.listErr = err, err
		e.expect(e.do("DELETE", path+"?image=core", "", true), code)
		if code != 400 {
			e.expect(e.do("GET", path, "", true), code)
		}
	}
	e.expect(e.do("GET", "/internal/git/repos/non-un-uuid/packages", "", true), 400)
}

// Senza registry le rotte non esistono.
func TestPackages_NonMontateSenzaRegistry(t *testing.T) {
	e, _ := newRegistryEnv(t, Deps{})
	e.expect(e.do("GET", "/internal/git/repos/"+rid+"/packages", "", true), http.StatusNotFound)
}
