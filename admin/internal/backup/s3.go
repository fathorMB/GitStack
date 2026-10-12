package backup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// ObjectInfo descrive un archivio sullo storage.
type ObjectInfo struct {
	Name         string // nome senza prefisso
	Size         int64
	LastModified time.Time
}

// ObjectStore è lo storage S3 visto dal backup: i nomi sono relativi al
// prefisso configurato.
type ObjectStore interface {
	// Check verifica raggiungibilità, credenziali e bucket.
	Check(ctx context.Context) error
	Put(ctx context.Context, name string, r io.Reader, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	List(ctx context.Context) ([]ObjectInfo, error)
	Remove(ctx context.Context, name string) error
}

// secretScrubber toglie dai messaggi le chiavi S3: un errore del client non
// deve mai portarle in un log, in `gitstack status` o in una notifica.
type secretScrubber struct{ secrets []string }

func newScrubber(c config.S3) secretScrubber {
	var s []string
	for _, v := range []string{c.SecretKey, c.AccessKey} {
		if len(v) >= 4 {
			s = append(s, v)
		}
	}
	return secretScrubber{secrets: s}
}

func (s secretScrubber) scrub(msg string) string {
	for _, v := range s.secrets {
		msg = strings.ReplaceAll(msg, v, "***")
	}
	return msg
}

func (s secretScrubber) err(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("s3 %s: %s", op, s.scrub(err.Error()))
}

type minioStore struct {
	cl     *minio.Client
	bucket string
	prefix string // con barra finale, o vuoto
	sc     secretScrubber
}

// NewS3Store apre lo storage con minio-go (S3 compatibile).
func NewS3Store(c config.S3) (ObjectStore, error) {
	sc := newScrubber(c)
	ep, secure := strings.TrimSpace(c.Endpoint), true
	if strings.Contains(ep, "://") {
		u, err := url.Parse(ep)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("endpoint %q non valido", ep)
		}
		secure = u.Scheme != "http"
		ep = u.Host
	}
	cl, err := minio.New(ep, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: secure,
		Region: c.RegionOrDefault(),
	})
	if err != nil {
		return nil, sc.err("client", err)
	}
	p := strings.Trim(c.Prefix, "/")
	if p != "" {
		p += "/"
	}
	return &minioStore{cl: cl, bucket: c.Bucket, prefix: p, sc: sc}, nil
}

func (m *minioStore) Check(ctx context.Context) error {
	ok, err := m.cl.BucketExists(ctx, m.bucket)
	if err != nil {
		return m.sc.err("verifica del bucket", err)
	}
	if !ok {
		return fmt.Errorf("s3: il bucket %q non esiste", m.bucket)
	}
	return nil
}

func (m *minioStore) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	_, err := m.cl.PutObject(ctx, m.bucket, m.prefix+name, r, size, minio.PutObjectOptions{
		ContentType:          "application/octet-stream",
		ServerSideEncryption: nil,
	})
	return m.sc.err("caricamento di "+name, err)
}

func (m *minioStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	o, err := m.cl.GetObject(ctx, m.bucket, m.prefix+name, minio.GetObjectOptions{})
	if err != nil {
		return nil, m.sc.err("download di "+name, err)
	}
	// GetObject è pigro: Stat fa emergere subito «non esiste».
	if _, err := o.Stat(); err != nil {
		_ = o.Close()
		return nil, m.sc.err("download di "+name, err)
	}
	return o, nil
}

func (m *minioStore) List(ctx context.Context) ([]ObjectInfo, error) {
	var out []ObjectInfo
	for o := range m.cl.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: m.prefix}) {
		if o.Err != nil {
			return nil, m.sc.err("elenco", o.Err)
		}
		name := strings.TrimPrefix(o.Key, m.prefix)
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		out = append(out, ObjectInfo{Name: name, Size: o.Size, LastModified: o.LastModified})
	}
	return out, nil
}

func (m *minioStore) Remove(ctx context.Context, name string) error {
	return m.sc.err("rimozione di "+name, m.cl.RemoveObject(ctx, m.bucket, m.prefix+name, minio.RemoveObjectOptions{}))
}

// IsArchiveName dice se è il nome di un archivio prodotto da `gitstack
// backup` (cifrato o no). Per i nomi dati dall'utente a restore --from s3
// vale anche come guardia: niente separatori di percorso.
func IsArchiveName(n string) bool {
	return n == path.Base(n) && n == filepath.Base(n) && strings.HasPrefix(n, ArchivePrefix) &&
		(strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tar.gz.enc"))
}

// S3Archives elenca gli archivi sullo storage, dal più vecchio al più nuovo
// (il nome contiene la data UTC).
func S3Archives(ctx context.Context, st ObjectStore) ([]ObjectInfo, error) {
	all, err := st.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []ObjectInfo
	for _, o := range all {
		if IsArchiveName(o.Name) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// UploadResult è l'esito di un caricamento.
type UploadResult struct {
	Name    string
	Size    int64
	Pruned  []string
	PruneEr error // la potatura non riuscita non invalida il caricamento
}

// Upload carica su S3 l'archivio appena prodotto (e il suo .sha256) e poi
// applica la conservazione di S3. L'archivio deve essere cifrato: lo si
// verifica sul file, non sulla fiducia.
func Upload(ctx context.Context, o *Options, st ObjectStore, archive string) (*UploadResult, error) {
	s3 := o.Cfg.Backup.S3
	if s3 == nil {
		return nil, errors.New("backup.s3 non configurato")
	}
	name := filepath.Base(archive)
	if !strings.HasSuffix(name, ".enc") {
		return nil, refused("l'archivio %s non è cifrato: su S3 si caricano solo archivi cifrati", name)
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if !isEncryptedFile(f) {
		return nil, refused("l'archivio %s non risulta cifrato: non lo carico su S3", name)
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	o.logf("carico %s su S3 (bucket %s)", name, s3.Bucket)
	if err := st.Put(ctx, name, f, fi.Size()); err != nil {
		return nil, err
	}
	if sum, err := os.ReadFile(archive + ".sha256"); err == nil {
		if err := st.Put(ctx, name+".sha256", strings.NewReader(string(sum)), int64(len(sum))); err != nil {
			return nil, err
		}
	}
	res := &UploadResult{Name: name, Size: fi.Size()}
	if keep := s3.KeepCount(); keep > 0 {
		res.Pruned, res.PruneEr = pruneS3(ctx, st, keep)
		for _, n := range res.Pruned {
			o.logf("rimosso da S3 l'archivio più vecchio: %s", n)
		}
	}
	return res, nil
}

func pruneS3(ctx context.Context, st ObjectStore, keep int) ([]string, error) {
	arcs, err := S3Archives(ctx, st)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for len(arcs) > keep {
		n := arcs[0].Name
		arcs = arcs[1:]
		if err := st.Remove(ctx, n); err != nil {
			errs = append(errs, err)
			continue
		}
		_ = st.Remove(ctx, n+".sha256") // può non esserci
		removed = append(removed, n)
	}
	return removed, errors.Join(errs...)
}

func isEncryptedFile(f *os.File) bool {
	defer func() { _, _ = f.Seek(0, io.SeekStart) }()
	_, _ = f.Seek(0, io.SeekStart)
	return IsEncrypted(bufio.NewReader(f))
}

func newSHA() hash.Hash { return sha256.New() }

func hexSum(c *countHash) string { return hex.EncodeToString(c.h.Sum(nil)) }

// FetchFromS3 scarica in dir l'archivio indicato (o il più recente se name
// è vuoto), ne verifica lo SHA-256 se c'è il file accanto, e ritorna il
// percorso locale.
func FetchFromS3(ctx context.Context, o *Options, st ObjectStore, name, dir string) (string, error) {
	if name == "" {
		arcs, err := S3Archives(ctx, st)
		if err != nil {
			return "", err
		}
		if len(arcs) == 0 {
			return "", refused("nessun archivio su S3 (bucket %s, prefisso %q)", o.Cfg.Backup.S3.Bucket, o.Cfg.Backup.S3.Prefix)
		}
		name = arcs[len(arcs)-1].Name
	}
	if !IsArchiveName(name) {
		return "", refused("%q non è il nome di un archivio di gitstack backup", name)
	}
	rc, err := st.Get(ctx, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	local := filepath.Join(dir, name)
	out, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	ch := &countHash{w: out, h: newSHA()}
	_, cerr := io.Copy(ch, rc)
	if e := out.Close(); cerr == nil {
		cerr = e
	}
	if cerr != nil {
		_ = os.Remove(local)
		return "", fmt.Errorf("download di %s: %w", name, cerr)
	}
	o.logf("scaricato %s (%d byte)", name, ch.n)
	if sr, err := st.Get(ctx, name+".sha256"); err == nil {
		b, _ := io.ReadAll(io.LimitReader(sr, 1<<10))
		_ = sr.Close()
		want := strings.Fields(string(b))
		got := hexSum(ch)
		if len(want) > 0 && !strings.EqualFold(want[0], got) {
			_ = os.Remove(local)
			return "", refused("SHA-256 di %s non corrisponde: archivio corrotto o alterato", name)
		}
	}
	return local, nil
}
