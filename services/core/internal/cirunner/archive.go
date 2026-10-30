package cirunner

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxExtractBytes è il tetto di byte estratti da un archivio (difesa da un archivio gonfiato).
const maxExtractBytes = 4 << 30

// extractTarGz estrae un tar.gz in dest togliendo la prima cartella (git archive --prefix).
// Accetta solo file e cartelle dentro dest; i collegamenti simbolici e fisici si saltano:
// un link verso l'esterno non deve poter scrivere fuori dal workspace.
//
// I modi seguono la semantica di git checkout, non quelli del tar: file 0644 (0755 se il tar
// ha un bit di esecuzione) e cartelle (radice compresa) 0755, prima della umask del processo. Devono essere
// leggibili da altri utenti: act copia il workspace conservando i modi e BuildKit li manda
// com'è, quindi un Dockerfile con COPY e poi USER non root (immagini del pilot, GIT-510,
// e2e MR-393-r1) cadrebbe con EACCES su file 0640 di proprietà di root.
func extractTarGz(r io.Reader, dest string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	var written int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		// Toglie la cartella radice del prefisso.
		_, rest, found := strings.Cut(name, "/")
		if !found {
			continue
		}
		if rest == "" || rest == "." || strings.HasPrefix(rest, "../") || rest == ".." || path.IsAbs(rest) {
			if rest != "" && rest != "." {
				return fmt.Errorf("percorso non valido nell'archivio: %q", h.Name)
			}
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(rest))
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("percorso fuori dal workspace: %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxExtractBytes-written+1))
			written += n
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if written > maxExtractBytes {
				return fmt.Errorf("archivio troppo grande")
			}
		default:
			// symlink, hardlink, dispositivi: saltati.
		}
	}
}
