package httpserver

import (
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/registry"
)

// GIT-395: la rotta interna puntuale della revision vuole la firma di servizio, passa image/tag/
// digest al registry, risponde con revision (anche vuota) e mappa 404, 400 e 500.
func TestPackages_RevisionPuntuale(t *testing.T) {
	f := &fakePackages{revision: revCommit}
	e, _ := newRegistryEnv(t, Deps{Packages: f})
	path := "/internal/git/repos/" + rid + "/packages/revision"

	e.expect(e.do("GET", path+"?tag=v1", "", false), 401)

	rec := e.do("GET", path+"?image="+url.QueryEscape("a/b")+"&tag=v1", "", true)
	e.expect(rec, 200)
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["revision"] != revCommit {
		t.Fatalf("risposta: %v %s", err, rec.Body.String())
	}
	if f.gotRepo != rid || f.gotImage != "a/b" || f.gotTag != "v1" || f.gotDigest != "" {
		t.Fatalf("parametri al registry: %+v", f)
	}
	dg := "sha256:" + "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"
	e.expect(e.do("GET", path+"?image=web&digest="+dg, "", true), 200)
	if f.gotDigest != dg || f.gotImage != "web" {
		t.Fatalf("digest non passato: %+v", f)
	}

	// Etichetta assente: revision vuota, ma 200 (il campo c'è sempre).
	f.revision = ""
	rec = e.do("GET", path+"?tag=v1", "", true)
	e.expect(rec, 200)
	out = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if v, ok := out["revision"]; !ok || v != "" {
		t.Fatalf("revision vuota attesa: %s", rec.Body.String())
	}

	e.expect(e.do("GET", path+"?image=web", "", true), 400) // né tag né digest
	for err, code := range map[error]int{
		registry.ErrPackageNotFound: 404, registry.ErrInvalidImage: 400, errors.New("disco"): 500,
	} {
		f.revErr = err
		e.expect(e.do("GET", path+"?tag=v1", "", true), code)
	}
}
