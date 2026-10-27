package registry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"path/filepath"
	"strings"
	"testing"

)

func pubPEM(t *testing.T, k *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// pushSigned pubblica un'immagine con una firma cosign (payload simplesigning) di key per il
// digest, con il docker-reference ref, come fa `cosign sign` verso il registry.
func pushCosignSig(t *testing.T, e *cleanEnv, image, digest, ref string, key *ecdsa.PrivateKey) {
	t.Helper()
	payload := `{"critical":{"identity":{"docker-reference":"` + ref + `"},"image":{"docker-manifest-digest":"` + digest + `"},"type":"cosign container image signature"}}`
	sum := sha256.Sum256([]byte(payload))
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	g := &authzGrant{grant: &grant{repoID: idApp, actions: map[string]bool{actionPush: true, actionPull: true}}, canonical: "alice/app"}
	rt := route{owner: "alice", repo: "app", image: image}
	desc := "application/vnd.oci.image.manifest.v1+json"
	if err := e.h.putArtifact(context.Background(), g, rt.upstreamName(idApp), digest, desc, 100, ".sig", []byte(payload),
		"application/vnd.dev.cosign.simplesigning.v1+json", map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig)}); err != nil {
		t.Fatal(err)
	}
}

// GIT-349 (H10): VerifyImage distingue la firma della CI di questo repo, quella ricopiata da un
// altro repo, una chiave fidata aggiuntiva e l'assenza di firma.
func TestVerifyImage_IstanzaFidataCopiataENessuna(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	ctx := context.Background()
	ci := e.mustPush("alice/app/web:sha-1", "gsj_rw") // firmata dalla CI
	hand := e.mustPush("alice/app/web:hand", "gst_write")
	dgHand := digestOf(t, hand).String()

	got, err := e.h.VerifyImage(ctx, idApp, "web", "sha-1", "", "alice/app", nil)
	if err != nil || got.Signer != SignerInstance || got.Digest != digestOf(t, ci).String() {
		t.Fatalf("firma della CI: %+v %v", got, err)
	}
	// Per digest, con un riferimento del repo scritto con altre maiuscole.
	if got, err = e.h.VerifyImage(ctx, idApp, "web", "", digestOf(t, ci).String(), "Alice/App", nil); err != nil || got.Signer != SignerInstance {
		t.Fatalf("per digest: %+v %v", got, err)
	}
	// Nessuna firma.
	if got, err = e.h.VerifyImage(ctx, idApp, "web", "hand", "", "alice/app", nil); err != nil || got.Signer != SignerNone {
		t.Fatalf("a mano: %+v %v", got, err)
	}
	// Firma dell'istanza che nomina un altro repo (ricopiata): non vale.
	if got, err = e.h.VerifyImage(ctx, idApp, "web", "sha-1", "", "bob/other", nil); err != nil || got.Signer != SignerNone {
		t.Fatalf("firma di un altro repo: %+v %v", got, err)
	}
	// Chiave aggiuntiva: la firma di una chiave non fidata non vale, quella fidata sì.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	trusted, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pushCosignSig(t, e, "web", dgHand, "ghcr.io/qualcuno/web", trusted)
	if got, err = e.h.VerifyImage(ctx, idApp, "web", "hand", "", "alice/app", [][]byte{pubPEM(t, other)}); err != nil || got.Signer != SignerNone {
		t.Fatalf("chiave non fidata: %+v %v", got, err)
	}
	if got, err = e.h.VerifyImage(ctx, idApp, "web", "hand", "", "alice/app", [][]byte{pubPEM(t, other), pubPEM(t, trusted)}); err != nil || got.Signer != SignerTrusted || got.KeyIndex != 1 {
		t.Fatalf("chiave fidata: %+v %v", got, err)
	}
	// Errori: tag assente, nome non valido, chiave non valida.
	if _, err = e.h.VerifyImage(ctx, idApp, "web", "nope", "", "alice/app", nil); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("tag assente: %v", err)
	}
	if _, err = e.h.VerifyImage(ctx, idApp, "Web!", "sha-1", "", "alice/app", nil); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("nome: %v", err)
	}
	if _, err = e.h.VerifyImage(ctx, idApp, "web", "sha-1", "", "alice/app", [][]byte{[]byte("no")}); !errors.Is(err, ErrInvalidTrustedKey) {
		t.Fatalf("chiave: %v", err)
	}
	// Un tag tecnico (.sig) non è un'immagine.
	if _, err = e.h.VerifyImage(ctx, idApp, "web", strings.Replace(dgHand, ":", "-", 1)+".sig", "", "alice/app", nil); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("tag tecnico: %v", err)
	}
}

// GIT-349 (H7): solo un push del job con il contesto del run notifica core, e solo per i tag veri.
func TestJobPush_NotificaCoreConIlRun(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	e.b.jobs["gsj_run"] = fakeJob{repoID: idApp, packages: "write", workflow: "ci", job: "image",
		runID: "run-1", ref: "refs/heads/main", event: "push", actor: "u-write"}
	e.mustPush("alice/app/web:sha-9", "gsj_run")
	e.mustPush("alice/app/web:a-mano", "gst_write") // non è un job
	e.mustPush("alice/app/web:senza-run", "gsj_rw") // job senza contesto del run

	e.b.mu.Lock()
	defer e.b.mu.Unlock()
	if len(e.b.pushes) != 1 {
		t.Fatalf("notifiche: %+v", e.b.pushes)
	}
	p := e.b.pushes[0]
	if p.Image != "web" || p.Tag != "sha-9" || p.Ref != "refs/heads/main" || p.Event != "push" || p.ActorID != "u-write" || p.RunID != "run-1" || !strings.HasPrefix(p.Digest, "sha256:") {
		t.Fatalf("notifica: %+v", p)
	}
}
