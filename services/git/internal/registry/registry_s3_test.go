package registry

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// fakeS3 è un S3 finto in memoria (gofakes3) con un bucket, in ascolto su http.
type fakeS3 struct {
	be  *s3mem.Backend
	srv *httptest.Server
}

func newFakeS3(t *testing.T, bucket string) *fakeS3 {
	t.Helper()
	be := s3mem.New()
	if err := be.CreateBucket(bucket); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(be).Server())
	t.Cleanup(srv.Close)
	return &fakeS3{be: be, srv: srv}
}

// keys elenca le chiavi del bucket sotto un prefisso.
func (f *fakeS3) keys(t *testing.T, bucket, prefix string) []string {
	t.Helper()
	list, err := f.be.ListBucket(bucket, &gofakes3.Prefix{HasPrefix: true, Prefix: prefix}, gofakes3.ListBucketPage{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range list.Contents {
		out = append(out, c.Key)
	}
	return out
}

// Archiviazione su S3 (criterio di GIT-286): stesso comportamento del disco, i dati finiscono
// nel bucket sotto il prefisso scelto e la cancellazione definitiva li toglie.
func TestArchiviazioneS3(t *testing.T) {
	fs3 := newFakeS3(t, "immagini")
	e := newEnvWith(t, func(c *Config) {
		c.Dir, c.testInMemory = "", false
		c.S3 = &S3{Bucket: "immagini", Region: "eu-west-1", Endpoint: fs3.srv.URL, AccessKey: "AK", SecretKey: "SK", PathStyle: true, Insecure: true, Prefix: "gs-reg"}
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	})

	img := e.mustPush("alice/app/web:1", "gst_write")
	want, _ := img.Digest()
	if got, err := e.pull("alice/app/web:1", "gst_read"); err != nil || got != want {
		t.Fatalf("pull da S3: %v %v", got, err)
	}
	_, err := e.push("alice/app/web:2", "gst_read")
	wantErr(t, "push con read su S3", err, 403)
	_, err = e.pull("alice/secret:1", "gst_read")
	wantErr(t, "repo non leggibile su S3", err, 404)

	all := fs3.keys(t, "immagini", "")
	if len(all) == 0 {
		t.Fatal("nessun oggetto nel bucket dopo il push")
	}
	for _, k := range all {
		if !strings.HasPrefix(k, "gs-reg/") {
			t.Errorf("oggetto fuori dal prefisso: %s", k)
		}
	}
	repoKeys := fs3.keys(t, "immagini", "gs-reg/docker/registry/v2/repositories/"+idApp)
	if len(repoKeys) == 0 {
		t.Fatalf("i repository devono stare sotto l'id del repo; chiavi: %v", all)
	}

	// Eliminato e ripristinato: le immagini tornano.
	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = true })
	_, err = e.pull("alice/app/web:1", "gst_read")
	wantErr(t, "repo eliminato su S3", err, 404)
	e.b.set("alice/app", func(r *fakeRepo) { r.deleted = false })
	if got, err := e.pull("alice/app/web:1", "gst_read"); err != nil || got != want {
		t.Fatalf("dopo il ripristino su S3: %v %v", got, err)
	}

	// Cancellazione definitiva.
	if err := e.h.Purge(context.Background(), idApp); err != nil {
		t.Fatal(err)
	}
	if left := fs3.keys(t, "immagini", "gs-reg/docker/registry/v2/repositories/"+idApp); len(left) != 0 {
		t.Errorf("dopo Purge restano %d oggetti del repo: %v", len(left), left)
	}
	_, err = e.pull("alice/app/web:1", "gst_read")
	wantErr(t, "pull dopo Purge su S3", err, 404)
}

func TestArchiviazioneS3_BucketInesistente(t *testing.T) {
	fs3 := newFakeS3(t, "altro")
	h, err := New(Config{S3: &S3{Bucket: "manca", Endpoint: fs3.srv.URL, AccessKey: "AK", SecretKey: "SK", PathStyle: true, Insecure: true}, Secret: "x"}, newBackend())
	if err != nil {
		return // errore subito: va bene
	}
	defer func() { _ = h.Close() }()
	// distribution crea il driver senza toccare la rete: il bucket mancante si vede alla prima richiesta.
	e := &env{t: t, b: newBackend(), h: h}
	e.srv = httptest.NewServer(h)
	defer e.srv.Close()
	_, err = e.push("alice/app:1", "gst_write")
	if err == nil {
		t.Error("il push in un bucket inesistente doveva fallire")
	}
}
