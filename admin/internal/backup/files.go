package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// tarDir scrive in w un tar con il contenuto di root (percorsi relativi).
// Conserva modo, proprietario e data; salta ciò che non è file, cartella o
// link simbolico (socket, device).
func tarDir(w io.Writer, root string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&fs.ModeSymlink == 0 {
			return nil
		}
		link := ""
		if mode&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if mode.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		n, err := io.Copy(tw, f)
		if err != nil {
			return err
		}
		if n != hdr.Size {
			return fmt.Errorf("%s: il file è cambiato durante la copia", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// emptyDir svuota root senza rimuoverla.
func emptyDir(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin unisce root e il nome di un'entry del tar rifiutando percorsi
// assoluti o che escono da root.
func safeJoin(root, name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." {
		return root, nil
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("percorso non valido nell'archivio: %q", name)
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

type dirMeta struct {
	path     string
	mode     fs.FileMode
	uid, gid int
	mtime    time.Time
}

// untarDir estrae il tar in root (che deve esistere). Rifiuta percorsi che
// escono da root e scritture attraverso link simbolici.
func untarDir(r io.Reader, root string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	var dirs []dirMeta
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		dst, err := safeJoin(root, hdr.Name)
		if err != nil {
			return err
		}
		if dst == root {
			continue
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(dst))
		if err != nil {
			return err
		}
		if parent != realRoot && !strings.HasPrefix(parent, realRoot+string(filepath.Separator)) {
			return fmt.Errorf("percorso fuori dal volume: %q", hdr.Name)
		}
		mode := fs.FileMode(hdr.Mode).Perm() | modeBits(hdr.Mode)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{dst, mode, hdr.Uid, hdr.Gid, hdr.ModTime})
		case tar.TypeReg:
			f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			if err := os.Chmod(dst, mode); err != nil {
				return err
			}
			if err := lchown(dst, hdr.Uid, hdr.Gid); err != nil {
				return err
			}
			_ = os.Chtimes(dst, hdr.ModTime, hdr.ModTime)
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return err
			}
			_ = lchown(dst, hdr.Uid, hdr.Gid)
		default:
			// Tipi non prodotti da tarDir: ignorati.
		}
	}
	// Le cartelle per ultime, dalla più profonda: modo e data non
	// ostacolano più la creazione dei figli.
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
		if err := lchown(d.path, d.uid, d.gid); err != nil {
			return err
		}
		_ = os.Chtimes(d.path, d.mtime, d.mtime)
	}
	return nil
}

// modeBits traduce setuid/setgid/sticky del tar nei bit di os.FileMode.
func modeBits(m int64) fs.FileMode {
	var f fs.FileMode
	if m&0o4000 != 0 {
		f |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		f |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		f |= fs.ModeSticky
	}
	return f
}
