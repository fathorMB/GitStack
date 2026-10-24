package ci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Archiviazione di artefatti e cache (CI5, N7): su disco, nel volume di core, oppure su S3.
// Le chiavi le genera core (artifacts/<repo>/<id>, caches/<repo>/<id>): mai nomi scelti dall'utente.

// ErrBlobNotFound: l'oggetto non c'è (già eliminato, o mai scritto).
var ErrBlobNotFound = errors.New("oggetto non trovato")

// ErrTooLarge: il flusso supera il limite di dimensione chiesto.
var ErrTooLarge = errors.New("oggetto troppo grande")

// Blobs è l'archivio degli oggetti di artefatti e cache.
type Blobs interface {
	// Put scrive r sotto key e ritorna i byte scritti. Se r ha più di max byte (max > 0)
	// ritorna ErrTooLarge e non lascia niente. Sostituisce un oggetto già presente.
	Put(ctx context.Context, key string, r io.Reader, max int64) (int64, error)
	// Open apre l'oggetto per la lettura; ErrBlobNotFound se manca.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete elimina l'oggetto; un oggetto che manca non è un errore.
	Delete(ctx context.Context, key string) error
}

// validBlobKey: solo segmenti semplici separati da /.
func validBlobKey(key string) error {
	if key == "" || len(key) > 512 || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, ":") {
		return fmt.Errorf("chiave non valida: %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("chiave non valida: %q", key)
		}
	}
	return nil
}

// DiskBlobs conserva gli oggetti come file sotto Dir.
type DiskBlobs struct{ Dir string }

func (d DiskBlobs) path(key string) (string, error) {
	if d.Dir == "" {
		return "", errors.New("directory di artefatti e cache non configurata")
	}
	if err := validBlobKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.Dir, filepath.FromSlash(path.Clean(key))), nil
}

// Put scrive in un file temporaneo nella stessa cartella e lo rinomina: una lettura non vede mai un file a metà.
func (d DiskBlobs) Put(ctx context.Context, key string, r io.Reader, max int64) (int64, error) {
	p, err := d.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return 0, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	n, err := copyLimited(ctx, tmp, r, max)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if err := os.Rename(name, p); err != nil {
		return 0, err
	}
	return n, nil
}

func (d DiskBlobs) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (d DiskBlobs) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// copyLimited copia r in w fino a max byte (max <= 0: senza limite); se r ne ha di più, ErrTooLarge.
func copyLimited(ctx context.Context, w io.Writer, r io.Reader, max int64) (int64, error) {
	var src io.Reader = &ctxReader{ctx: ctx, r: r}
	if max > 0 {
		src = io.LimitReader(src, max+1)
	}
	n, err := io.Copy(w, src)
	if err != nil {
		return n, err
	}
	if max > 0 && n > max {
		return n, ErrTooLarge
	}
	return n, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
