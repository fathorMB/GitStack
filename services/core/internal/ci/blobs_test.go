package ci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 è un S3 finto path-style in memoria: PUT, GET e DELETE di <bucket>/<chiave>. Controlla che le
// richieste siano firmate SigV4 (credenziale, intestazioni firmate) e che x-amz-content-sha256 sia lo SHA-256
// vero del corpo.
type fakeS3 struct {
	t       *testing.T
	bucket  string
	access  string
	mu      sync.Mutex
	objects map[string][]byte
	puts    int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+f.access+"/") ||
		!strings.Contains(auth, "/us-east-1/s3/aws4_request") ||
		!strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") ||
		!strings.Contains(auth, "Signature=") || r.Header.Get("x-amz-date") == "" {
		http.Error(w, "firma assente", http.StatusForbidden)
		return
	}
	prefix := "/" + f.bucket + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, "bucket sbagliato", http.StatusNotFound)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, prefix)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
			http.Error(w, "x-amz-content-sha256 non corrisponde", http.StatusBadRequest)
			return
		}
		f.objects[key] = body
		f.puts++
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "metodo", http.StatusMethodNotAllowed)
	}
}

func newFakeS3(t *testing.T) (*fakeS3, *S3Blobs) {
	t.Helper()
	f := &fakeS3{t: t, bucket: "ci", access: "AKIATEST", objects: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b, err := NewS3Blobs(S3Config{Endpoint: srv.URL, Bucket: "ci", Prefix: "gs/", AccessKey: "AKIATEST", SecretKey: "segreto",
		SpoolDir: t.TempDir(), Now: func() time.Time { return time.Date(2026, 10, 24, 1, 2, 3, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

// blobSuite è il contratto di Blobs, uguale su disco e su S3.
func blobSuite(t *testing.T, b Blobs) {
	t.Helper()
	ctx := context.Background()
	key := "artifacts/r1/a1"
	n, err := b.Put(ctx, key, strings.NewReader("contenuto"), 100)
	if err != nil || n != 9 {
		t.Fatalf("Put = %d, %v", n, err)
	}
	rc, err := b.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "contenuto" {
		t.Errorf("letto %q", got)
	}
	// Sostituisce.
	if _, err := b.Put(ctx, key, strings.NewReader("nuovo"), 100); err != nil {
		t.Fatal(err)
	}
	rc, _ = b.Open(ctx, key)
	got, _ = io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "nuovo" {
		t.Errorf("dopo la sostituzione: %q", got)
	}
	// Troppo grande: errore e niente al posto di quello vecchio.
	if _, err := b.Put(ctx, "artifacts/r1/grande", bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oltre il limite: %v", err)
	}
	if _, err := b.Open(ctx, "artifacts/r1/grande"); !errors.Is(err, ErrBlobNotFound) {
		t.Errorf("un oggetto rifiutato non deve esistere: %v", err)
	}
	// Vuoto.
	if n, err := b.Put(ctx, "caches/r1/vuota", strings.NewReader(""), 10); err != nil || n != 0 {
		t.Errorf("oggetto vuoto: %d, %v", n, err)
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(ctx, key); !errors.Is(err, ErrBlobNotFound) {
		t.Errorf("dopo Delete: %v", err)
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Errorf("Delete di un oggetto mancante non è un errore: %v", err)
	}
	for _, bad := range []string{"", "../x", "a/../b", "/abs", "a//b", `a\b`, "c:/x"} {
		if _, err := b.Put(ctx, bad, strings.NewReader("x"), 10); err == nil {
			t.Errorf("la chiave %q doveva essere rifiutata", bad)
		}
	}
}

func TestDiskBlobs(t *testing.T) { blobSuite(t, DiskBlobs{Dir: t.TempDir()}) }

func TestDiskBlobsSenzaCartella(t *testing.T) {
	if _, err := (DiskBlobs{}).Put(context.Background(), "a/b", strings.NewReader("x"), 0); err == nil {
		t.Error("senza cartella configurata Put deve fallire")
	}
}

func TestS3BlobsConS3Finto(t *testing.T) {
	f, b := newFakeS3(t)
	blobSuite(t, b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts == 0 {
		t.Error("nessun PUT è arrivato all'S3 finto")
	}
	// Il prefisso configurato si applica a ogni chiave.
	if _, ok := f.objects["gs/caches/r1/vuota"]; !ok {
		t.Errorf("oggetti sull'S3: %v", f.objects)
	}
}

func TestS3BlobsRifiutaFirmaAssente(t *testing.T) {
	f, b := newFakeS3(t)
	f.access = "ALTRA-CHIAVE"
	if _, err := b.Put(context.Background(), "a/b", strings.NewReader("x"), 10); err == nil {
		t.Error("l'S3 che rifiuta la firma deve dare errore")
	}
}

func TestNewS3BlobsConfigNonValida(t *testing.T) {
	for _, c := range []S3Config{
		{Endpoint: "", Bucket: "b", AccessKey: "a", SecretKey: "s"},
		{Endpoint: "ftp://x", Bucket: "b", AccessKey: "a", SecretKey: "s"},
		{Endpoint: "http://x", Bucket: "", AccessKey: "a", SecretKey: "s"},
		{Endpoint: "http://x", Bucket: "b", AccessKey: "", SecretKey: "s"},
	} {
		if _, err := NewS3Blobs(c); err == nil {
			t.Errorf("configurazione accettata: %+v", c)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	if f, _ := DefaultSettings().Validate(); f != "" {
		t.Fatalf("i default devono essere validi: %s", f)
	}
	d := DefaultSettings()
	if d.LogRetentionDays != 90 || d.ArtifactRetentionDays != 30 || d.ArtifactMaxBytes != 2<<30 || d.CacheQuotaBytes != 10<<30 || d.CacheUnusedDays != 7 {
		t.Errorf("default di CI5: %+v", d)
	}
	for field, mut := range map[string]func(*Settings){
		"logRetentionDays":      func(s *Settings) { s.LogRetentionDays = 0 },
		"artifactRetentionDays": func(s *Settings) { s.ArtifactRetentionDays = 3651 },
		"artifactMaxBytes":      func(s *Settings) { s.ArtifactMaxBytes = 0 },
		"cacheQuotaBytes":       func(s *Settings) { s.CacheQuotaBytes = -1 },
		"cacheUnusedDays":       func(s *Settings) { s.CacheUnusedDays = 0 },
	} {
		s := DefaultSettings()
		mut(&s)
		if got, _ := s.Validate(); got != field {
			t.Errorf("campo atteso %s, trovato %q", field, got)
		}
	}
}

func TestValidArtifactName(t *testing.T) {
	for name, want := range map[string]bool{"dist": true, "coverage report": true, "": false, "a\\b": false, "x\ny": false, strings.Repeat("a", 256): false} {
		if ValidArtifactName(name) != want {
			t.Errorf("ValidArtifactName(%q) != %v", name, want)
		}
	}
}
