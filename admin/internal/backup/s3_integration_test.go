//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// Questi test girano contro un MinIO vero, avviato col CLI di Docker (non
// con testcontainers-go: il modulo admin è un binario distribuito e non
// deve portarsi dietro l'albero di dipendenze di Docker solo per un test).
// Si lanciano con: go test -tags integration -v -count=1 ./internal/backup/
// Senza Docker i test falliscono (non si saltano): un test saltato sembra
// passato.

const (
	// Docker Hub non pubblica più minio/minio e quay.io chiede il login: di
	// default l'immagine Chainguard; GITSTACK_TEST_MINIO_IMAGE la sostituisce.
	minioImage  = "cgr.dev/chainguard/minio:latest"
	minioAccess = "minioadmin-test"
	minioSecret = "minioadmin-secret-test-0123"
)

// startMinio avvia il container e ritorna l'endpoint host:porta.
func startMinio(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker non trovato nel PATH: %v", err)
	}
	image := minioImage
	if v := os.Getenv("GITSTACK_TEST_MINIO_IMAGE"); v != "" {
		image = v
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	name := "gitstack-test-minio-" + hex.EncodeToString(rnd[:])
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1::9000",
		"-e", "MINIO_ROOT_USER="+minioAccess, "-e", "MINIO_ROOT_PASSWORD="+minioSecret,
		image, "server", "/data").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	pout, err := exec.Command("docker", "port", name, "9000/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	// «127.0.0.1:49153» (eventualmente anche una riga IPv6).
	line := strings.Fields(string(pout))[0]
	_, port, err := net.SplitHostPort(line)
	if err != nil {
		t.Fatalf("porta non leggibile: %q", pout)
	}
	ep := "127.0.0.1:" + port
	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, err := http.Get("http://" + ep + "/minio/health/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return ep
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("MinIO non pronto: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func makeBucket(t *testing.T, ep, bucket string) {
	t.Helper()
	cl, err := minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(minioAccess, minioSecret, "")})
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
}

func rawObject(t *testing.T, ep, bucket, key string) []byte {
	t.Helper()
	cl, _ := minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(minioAccess, minioSecret, "")})
	o, err := cl.GetObject(context.Background(), bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	b, err := io.ReadAll(o)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestS3MinIO_BackupCifratoConservazioneERestore(t *testing.T) {
	ep := startMinio(t)
	const bucket = "gitstack-backups"
	makeBucket(t, ep, bucket)
	s3c := config.S3{Endpoint: "http://" + ep, Bucket: bucket, Prefix: "prod", AccessKey: minioAccess,
		SecretKey: minioSecret, KeyFile: "/etc/gitstack/backup.key", Retention: "2"}
	if err := s3c.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := NewS3Store(s3c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Check(ctx); err != nil {
		t.Fatal(err)
	}
	key := []byte("una-chiave-di-prova-lunga-abbastanza")

	e := newEnv(t)
	e.o.Key = key
	e.o.Cfg.Backup.S3 = &s3c
	var last *Result
	for i := 0; i < 3; i++ {
		at := time.Date(2026, 10, 6, 1, 2, i, 0, time.UTC)
		e.o.Now = func() time.Time { return at }
		res, err := Backup(ctx, e.o)
		if err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
		up, err := Upload(ctx, e.o, st, res.Path)
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		if up.PruneEr != nil {
			t.Fatalf("potatura: %v", up.PruneEr)
		}
		last = res
	}

	// Conservazione S3: solo gli ultimi 2 (più i loro .sha256), sotto il prefisso.
	arcs, err := S3Archives(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(arcs) != 2 {
		t.Fatalf("su S3 attesi 2 archivi: %+v", arcs)
	}
	all, _ := st.List(ctx)
	if len(all) != 4 {
		t.Errorf("attesi 2 archivi + 2 sha256, trovati %d: %+v", len(all), all)
	}
	if arcs[1].Name != filepath.Base(last.Path) {
		t.Errorf("il più recente deve essere %s: %+v", filepath.Base(last.Path), arcs)
	}

	// Cifrato: l'oggetto ha il magic, non contiene nulla in chiaro, ed è
	// byte per byte l'archivio locale.
	raw := rawObject(t, ep, bucket, "prod/"+arcs[1].Name)
	if !bytes.HasPrefix(raw, encMagic) {
		t.Error("l'oggetto su S3 non è cifrato")
	}
	for _, plain := range []string{"CHIAVE-CA", "OGGETTO", "database.sql", "manifest.json"} {
		if bytes.Contains(raw, []byte(plain)) {
			t.Errorf("%q in chiaro su S3", plain)
		}
	}
	local, _ := os.ReadFile(last.Path)
	if !bytes.Equal(raw, local) {
		t.Error("l'oggetto su S3 non coincide con l'archivio locale")
	}

	// Restore da S3 su un ambiente pulito (nessun archivio locale).
	clean := newEnv(t)
	clean.o.Key = key
	clean.o.Cfg.Backup.S3 = &s3c
	for _, entry := range []string{"alice/repo.git/HEAD"} {
		if _, err := os.Stat(filepath.Join(clean.git, filepath.FromSlash(entry))); err == nil {
			_ = os.Remove(filepath.Join(clean.git, filepath.FromSlash(entry)))
		}
	}
	p, err := FetchFromS3(ctx, clean.o, st, "", clean.dest)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != arcs[1].Name {
		t.Errorf("scaricato %s, atteso il più recente %s", p, arcs[1].Name)
	}
	if err := Restore(ctx, clean.o, p); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(clean.git, "alice", "repo.git", "objects", "ab", "cdef"))
	if err != nil || string(got) != "OGGETTO" {
		t.Errorf("repo ripristinato: %q %v", got, err)
	}

	// Chiave sbagliata: rifiutato, non un errore generico.
	bad := newEnv(t)
	bad.o.Key = []byte("un'altra-chiave-sbagliata-ma-lunga")
	p2, err := FetchFromS3(ctx, bad.o, st, arcs[0].Name, bad.dest)
	if err != nil {
		t.Fatal(err)
	}
	var ref *RefusedError
	if err := Restore(ctx, bad.o, p2); !errors.As(err, &ref) {
		t.Errorf("chiave sbagliata: %v", err)
	}

	// Chiavi S3 mai nei log di avanzamento.
	for _, l := range []string{e.log.String(), clean.log.String(), bad.log.String()} {
		if strings.Contains(l, minioSecret) || strings.Contains(l, minioAccess) {
			t.Errorf("chiave S3 nel log: %s", l)
		}
	}

	// Credenziali sbagliate: errore senza la chiave.
	wrong := s3c
	wrong.SecretKey = "chiave-segreta-sbagliata-123"
	ws, _ := NewS3Store(wrong)
	err = ws.Check(ctx)
	if err == nil {
		t.Fatal("credenziali sbagliate accettate")
	}
	if strings.Contains(err.Error(), wrong.SecretKey) {
		t.Errorf("errore con la chiave: %v", err)
	}
}
