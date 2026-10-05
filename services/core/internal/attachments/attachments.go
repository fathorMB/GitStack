// Package attachments tiene i file degli allegati di issue e commenti (I9,
// M-05/G): il tipo vero dai byte, il volume su disco e la pulizia di quelli
// mai collegati. I metadati stanno in core.issue_attachments (internal/store).
package attachments

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Valori di default dei limiti (configurabili da ambiente, vedi config).
const (
	DefaultMaxBytes int64 = 10 << 20 // 10 MiB
)

// Tipi serviti. Sono gli unici valori che finiscono in Content-Type: mai il
// tipo dichiarato dal client né l'estensione.
const (
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeGIF  = "image/gif"
	TypeWebP = "image/webp"
	TypePDF  = "application/pdf"
	TypeText = "text/plain; charset=utf-8"
	TypeZIP  = "application/zip"
)

// ErrUnsupported: i byte non sono di un tipo ammesso.
var ErrUnsupported = errors.New("tipo di allegato non ammesso")

// ErrTooLarge: il file supera il limite.
var ErrTooLarge = errors.New("allegato oltre il limite")

// Detect ricava il tipo dai primi byte (almeno i primi 512, se ci sono).
// Ammessi: PNG, JPEG, GIF, WebP, PDF, ZIP e testo (log compresi). HTML, XML e
// SVG non sono ammessi, nemmeno come testo: un allegato non è mai una pagina.
func Detect(head []byte) (string, error) {
	if len(head) == 0 {
		return "", ErrUnsupported
	}
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return TypePDF, nil
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return TypeZIP, nil
	}
	sniffed := http.DetectContentType(head)
	switch sniffed {
	case "image/png":
		return TypePNG, nil
	case "image/jpeg":
		return TypeJPEG, nil
	case "image/gif":
		return TypeGIF, nil
	case "image/webp":
		return TypeWebP, nil
	}
	if strings.HasPrefix(sniffed, "text/plain") {
		// DetectContentType non riconosce <svg: lo cerchiamo noi, come un
		// documento XML o HTML che cominci dopo spazi o BOM.
		lower := strings.ToLower(string(head))
		trimmed := strings.TrimLeft(strings.TrimPrefix(lower, "\xef\xbb\xbf"), " \t\r\n")
		if strings.HasPrefix(trimmed, "<") || strings.Contains(lower, "<svg") {
			return "", ErrUnsupported
		}
		return TypeText, nil
	}
	return "", ErrUnsupported
}

// Disk è il volume degli allegati: <dir>/<repo_id>/<id>, senza il nome
// originale. Il volume è quello del backup (D19).
type Disk struct {
	Dir string
}

func (d *Disk) path(repoID, id uuid.UUID) string {
	return filepath.Join(d.Dir, repoID.String(), id.String())
}

// Save legge r fino a max byte (ErrTooLarge oltre), controlla il tipo dai
// byte e, se ammesso, scrive il file in <repo_id>/<id>. Ritorna tipo e
// dimensione. In caso di errore non lascia file.
func (d *Disk) Save(repoID, id uuid.UUID, r io.Reader, max int64) (string, int64, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, err
	}
	head = head[:n]
	ctype, err := Detect(head)
	if err != nil {
		return "", 0, err
	}
	dir := filepath.Join(d.Dir, repoID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("cartella degli allegati: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("file temporaneo: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	// Un byte oltre il limite basta per rifiutare, senza leggere il resto.
	size, err := io.Copy(tmp, io.LimitReader(io.MultiReader(bytes.NewReader(head), r), max+1))
	if err != nil {
		return "", 0, err
	}
	if size > max {
		return "", 0, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpName, d.path(repoID, id)); err != nil {
		return "", 0, err
	}
	ok = true
	return ctype, size, nil
}

// Open apre il file di un allegato.
func (d *Disk) Open(repoID, id uuid.UUID) (*os.File, error) {
	return os.Open(d.path(repoID, id))
}

// Remove toglie il file di un allegato; già assente non è un errore.
func (d *Disk) Remove(repoID, id uuid.UUID) error {
	if err := os.Remove(d.path(repoID, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveRepo toglie tutti i file di un repo (cancellazione definitiva).
func (d *Disk) RemoveRepo(repoID uuid.UUID) error {
	return os.RemoveAll(filepath.Join(d.Dir, repoID.String()))
}
