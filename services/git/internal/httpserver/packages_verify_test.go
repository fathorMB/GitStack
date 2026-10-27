package httpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/registry"
)

// verPackages registra la richiesta di verifica e risponde con un esito fisso.
type verPackages struct {
	revPackages
	got struct {
		repoID, image, tag, digest, repo string
		keys                             [][]byte
	}
}

func (v *verPackages) VerifyImage(_ context.Context, repoID, image, tag, digest, repoRef string, trusted [][]byte) (registry.SignatureCheck, error) {
	v.got.repoID, v.got.image, v.got.tag, v.got.digest, v.got.repo, v.got.keys = repoID, image, tag, digest, repoRef, trusted
	if tag == "manca" {
		return registry.SignatureCheck{}, registry.ErrPackageNotFound
	}
	if len(trusted) == 1 && string(trusted[0]) == "storta" {
		return registry.SignatureCheck{}, registry.ErrInvalidTrustedKey
	}
	return registry.SignatureCheck{Digest: "sha256:aa", Signer: registry.SignerInstance}, nil
}

// GIT-349 (H10): la rotta interna di verifica passa repo, immagine, tag e chiavi al registry.
func TestPackages_VerificaLaFirma(t *testing.T) {
	v := &verPackages{}
	e, _ := newRegistryEnv(t, Deps{Packages: v})
	body := `{"image":"web","tag":"sha-1","repo":"alice/app","trustedKeys":["K1"]}`
	rec := e.do("POST", "/internal/git/repos/"+rid+"/packages/verify", body, true)
	e.expect(rec, 200)
	var out registry.SignatureCheck
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Signer != "instance" || out.Digest != "sha256:aa" {
		t.Fatalf("risposta: %v %s", err, rec.Body.String())
	}
	if v.got.image != "web" || v.got.tag != "sha-1" || v.got.repo != "alice/app" || len(v.got.keys) != 1 || string(v.got.keys[0]) != "K1" {
		t.Fatalf("richiesta: %+v", v.got)
	}
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/packages/verify", `{"image":"web","tag":"manca","repo":"alice/app"}`, true), 404)
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/packages/verify", `{"tag":"x","repo":"alice/app","trustedKeys":["storta"]}`, true), 400)
	e.expect(e.do("POST", "/internal/git/repos/"+rid+"/packages/verify", `{"image":"web"}`, true), 400)
}
