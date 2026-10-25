package registry

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func (e *cleanEnv) packages() map[string]Package {
	e.t.Helper()
	items, err := e.h.ListPackages(context.Background(), idApp)
	if err != nil {
		e.t.Fatalf("ListPackages: %v", err)
	}
	out := map[string]Package{}
	for _, p := range items {
		out[p.Image] = p
	}
	return out
}

func tagDetail(t *testing.T, p Package, tag string) PackageTag {
	t.Helper()
	for _, d := range p.TagDetails {
		if d.Name == tag {
			return d
		}
	}
	t.Fatalf("tag %s assente in %+v", tag, p.TagDetails)
	return PackageTag{}
}

// GIT-361: firma verificata con la chiave dell'istanza, SBOM, chi ha pubblicato, tag protetti.
func TestListPackages_FirmaSbomEAutore(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	e.sb.set(idApp, func(s *Settings) { s.ProtectedTagPatterns = []string{"v*"} })
	signed := e.mustPush("alice/app/ci:v1", "gsj_rw")         // il job firma e allega lo SBOM
	manual := e.mustPush("alice/app/hand:debug", "gst_write") // a mano: nessuna firma
	e.mustPush("alice/app/hand:debug2", "gst_admin")
	e.mustPush("alice/app/bot:x", "gst_write")
	e.b.mu.Lock()
	p := e.b.tokens["gst_write"]
	p.Kind = "agent"
	e.b.tokens["gst_write"] = p
	e.b.mu.Unlock()
	e.mustPush("alice/app/bot:y", "gst_write")

	pk := e.packages()
	if len(pk) != 3 {
		t.Fatalf("immagini: %d (%v)", len(pk), pk)
	}
	ci := pk["ci"]
	if ci.Signature != SignatureVerified || !ci.HasSbom || ci.Kind != "image" || ci.Digest != digestOf(t, signed).String() {
		t.Fatalf("ci: %+v", ci)
	}
	if ci.SizeBytes < 4096 || ci.PushedBy == nil || ci.PushedBy.Kind != PusherJob || ci.PushedBy.Login != "publish / image" {
		t.Fatalf("ci dimensione/autore: %+v", ci)
	}
	if len(ci.Tags) != 1 || ci.Tags[0] != "v1" {
		t.Fatalf("i tag tecnici non devono comparire: %v", ci.Tags)
	}
	if d := tagDetail(t, ci, "v1"); !d.Protected || d.Signature != SignatureVerified || !d.HasSbom || d.PushedAt.IsZero() {
		t.Fatalf("tag v1: %+v", d)
	}
	hand := pk["hand"]
	if hand.Signature != SignatureUnverified || hand.HasSbom {
		t.Fatalf("a mano: %+v", hand)
	}
	if d := tagDetail(t, hand, "debug"); d.Digest != digestOf(t, manual).String() || d.Protected ||
		d.PushedBy == nil || d.PushedBy.Kind != PusherUser || d.PushedBy.Login != "u-write" {
		t.Fatalf("tag debug: %+v", d)
	}
	if d := tagDetail(t, hand, "debug2"); d.PushedBy == nil || d.PushedBy.Login != "u-admin" {
		t.Fatalf("tag debug2: %+v", d)
	}
	bot := pk["bot"]
	if d := tagDetail(t, bot, "y"); d.PushedBy == nil || d.PushedBy.Kind != PusherAgent {
		t.Fatalf("tag y: %+v", d)
	}
	if d := tagDetail(t, bot, "x"); d.PushedBy == nil || d.PushedBy.Kind != PusherUser {
		t.Fatalf("tag x: %+v", d)
	}
}

// I metadati dei push precedenti a GIT-366 restano leggibili senza modificarne il login.
func TestListPackages_AutoreJobStorico(t *testing.T) {
	e := newCleanEnv(t, nil)
	e.mustPush("alice/app/legacy:v1", "gst_write")
	e.h.recordPusher(context.Background(), idApp+"/legacy", "v1", Pusher{Kind: PusherJob, Login: JobLogin})

	legacy := e.packages()["legacy"]
	if legacy.PushedBy == nil || legacy.PushedBy.Kind != PusherJob || legacy.PushedBy.Login != JobLogin {
		t.Fatalf("autore storico: %+v", legacy.PushedBy)
	}
}

// Una firma copiata da un'altra immagine, o fatta con un'altra chiave, non vale; un SBOM
// pubblicato a mano per quel digest sì (è un artefatto che il registry conserva).
func TestListPackages_FirmaNonValidaENessunaFirma(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	a := e.mustPush("alice/app/ci:a", "gsj_rw")
	b := e.mustPush("alice/app/ci:b", "gst_write") // a mano
	sigTag := strings.Replace(digestOf(t, a).String(), ":", "-", 1) + ".sig"
	sbomTag := strings.Replace(digestOf(t, a).String(), ":", "-", 1) + ".sbom"
	// Copia la firma di A come firma di B: il payload nomina il digest di A.
	desc, err := remote.Get(e.ref("alice/app/ci:"+sigTag), basic("gst_read"))
	if err != nil {
		t.Fatal(err)
	}
	fake := strings.Replace(digestOf(t, b).String(), ":", "-", 1) + ".sig"
	if err := remote.Tag(e.ref("alice/app/ci:"+fake).Context().Tag(fake), desc, basic("gst_write")); err != nil {
		t.Fatalf("tag della firma copiata: %v", err)
	}
	// Lo SBOM di A, copiato come SBOM di B.
	sb, err := remote.Get(e.ref("alice/app/ci:"+sbomTag), basic("gst_read"))
	if err != nil {
		t.Fatal(err)
	}
	fakeSbom := strings.Replace(digestOf(t, b).String(), ":", "-", 1) + ".sbom"
	if err := remote.Tag(e.ref("alice/app/ci:"+fakeSbom).Context().Tag(fakeSbom), sb, basic("gst_write")); err != nil {
		t.Fatalf("tag dello SBOM copiato: %v", err)
	}
	ci := e.packages()["ci"]
	da, db := tagDetail(t, ci, "a"), tagDetail(t, ci, "b")
	if da.Signature != SignatureVerified || !da.HasSbom {
		t.Fatalf("a: %+v", da)
	}
	if db.Signature != SignatureUnverified {
		t.Fatalf("la firma copiata da A non deve valere per B: %+v", db)
	}
	if !db.HasSbom {
		t.Fatalf("lo SBOM presente per il digest di B va contato: %+v", db)
	}
	if !slices.Contains(ci.Tags, "a") || !slices.Contains(ci.Tags, "b") || len(ci.Tags) != 2 {
		t.Fatalf("tag: %v", ci.Tags)
	}
}

func TestDeletePackage_TagImmagineProtettiEInesistenti(t *testing.T) {
	e := newCleanEnv(t, func(c *Config) { c.SigningKeyFile = filepath.Join(t.TempDir(), "cosign.key") })
	e.sb.set(idApp, func(s *Settings) { s.ProtectedTagPatterns = []string{"v*"} })
	ctx := context.Background()
	rel := e.mustPush("alice/app/ci:v1", "gsj_rw")
	e.mustPush("alice/app/ci:dev", "gst_write")
	other := e.mustPush("alice/app/other:t1", "gst_write")
	e.mustPush("alice/app/other:t2", "gst_write")

	// Inesistenti: 404; nome non valido: errore dedicato.
	for _, c := range []struct{ image, tag string }{{"nope", ""}, {"ci", "zzz"}, {"", ""}} {
		if err := e.h.DeletePackage(ctx, idApp, c.image, c.tag); !errors.Is(err, ErrPackageNotFound) {
			t.Fatalf("delete %+v: %v", c, err)
		}
	}
	if err := e.h.DeletePackage(ctx, idApp, "../x", ""); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("nome non valido: %v", err)
	}
	if err := e.h.DeletePackage(ctx, idSecret, "ci", ""); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("immagine di un altro repo: %v", err)
	}
	// Protetto: né il tag né l'immagine intera (che lo contiene) si eliminano.
	if err := e.h.DeletePackage(ctx, idApp, "ci", "v1"); !errors.Is(err, ErrProtectedTag) {
		t.Fatalf("tag protetto: %v", err)
	}
	if err := e.h.DeletePackage(ctx, idApp, "ci", ""); !errors.Is(err, ErrProtectedTag) {
		t.Fatalf("immagine con tag protetto: %v", err)
	}
	if got := e.packages()["ci"].Tags; len(got) != 2 {
		t.Fatalf("niente doveva sparire: %v", got)
	}
	// Un tag non protetto sì; l'immagine resta con l'altro.
	if err := e.h.DeletePackage(ctx, idApp, "ci", "dev"); err != nil {
		t.Fatal(err)
	}
	if got := e.packages()["ci"].Tags; len(got) != 1 || got[0] != "v1" {
		t.Fatalf("dopo delete di dev: %v", got)
	}
	if !e.manifestExists("alice/app/ci", digestOf(t, rel)) {
		t.Fatal("v1 è sparita")
	}
	// Immagine intera: sparisce dall'elenco, col manifest, la firma e lo SBOM.
	if err := e.h.DeletePackage(ctx, idApp, "other", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.packages()["other"]; ok {
		t.Fatal("other è ancora nell'elenco")
	}
	if e.manifestExists("alice/app/other", digestOf(t, other)) {
		t.Fatal("il manifest di other esiste ancora")
	}
	// Tolta la protezione, anche v1 va, con firma e SBOM.
	e.sb.set(idApp, func(s *Settings) { s.ProtectedTagPatterns = nil })
	if err := e.h.DeletePackage(ctx, idApp, "ci", ""); err != nil {
		t.Fatal(err)
	}
	if len(e.packages()) != 0 {
		t.Fatalf("restano: %v", e.packages())
	}
	if tags := e.tags("alice/app/ci"); len(tags) != 0 {
		t.Fatalf("restano tag tecnici: %v", tags)
	}
	// Un repo sconosciuto a core: 404.
	if _, err := e.h.ListPackages(context.Background(), "99999999-9999-4999-8999-999999999999"); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("repo sconosciuto: %v", err)
	}
}
