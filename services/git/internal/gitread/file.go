package gitread

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

// sniffBytes è quanto del file si guarda per riconoscerne il tipo.
const sniffBytes = 8000

// File è il contenuto di un file (FileContent del contratto, regola B1).
type File struct {
	Ref        string `json:"ref"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	SHA        string `json:"sha"`
	Size       int64  `json:"size"`
	Binary     bool   `json:"binary"`
	Kind       string `json:"kind"` // text, image, binary
	MimeType   string `json:"mimeType,omitempty"`
	Display    string `json:"display"` // highlight, plain, image, download
	Truncated  bool   `json:"truncated"`
	Encoding   string `json:"encoding,omitempty"`
	Content    string `json:"content,omitempty"`
	LastCommit Commit `json:"lastCommit"`
}

// File legge il file p a ref con le regole B1: testo fino a 1 MB intero da
// evidenziare, fino a 5 MB intero come testo semplice, oltre nessun contenuto
// (truncated); immagini PNG/JPEG/GIF/WebP/SVG riconosciute dal contenuto,
// inline in base64 fino a 1 MB; ogni altro file senza contenuto. ErrNotFound
// se p non esiste o non è un file.
func (s *Service) File(ctx context.Context, repoID, ref, p string) (*File, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	fp, err := gitref.ValidatePath(p, true)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		return nil, err
	}
	it, err := s.entryAt(ctx, dir, sha, fp)
	if err != nil {
		return nil, err
	}
	if it.Type != "blob" {
		return nil, ErrNotFound
	}
	return s.fileOf(ctx, dir, sha, ref, *it)
}

func (s *Service) fileOf(ctx context.Context, dir, commitSHA, ref string, it TreeItem) (*File, error) {
	f := &File{Ref: ref, Path: it.Path, Name: path.Base(it.Path), SHA: it.SHA, Size: it.Size}

	var data []byte
	var head []byte
	var err error
	if it.Size <= FilePlainMaxBytes {
		data, err = s.run.Output(ctx, dir, nil, "cat-file", "blob", it.SHA)
		if err != nil {
			return nil, err
		}
		head = data
	} else {
		head, err = s.blobHead(ctx, dir, it.SHA, sniffBytes)
		if err != nil {
			return nil, err
		}
	}
	kind, mime := classify(head)
	if kind == "text" && data != nil && !utf8.Valid(data) {
		kind = "binary"
	}
	f.Kind, f.MimeType, f.Binary = kind, mime, kind != "text"

	switch kind {
	case "text":
		switch {
		case it.Size <= FileHighlightMaxBytes:
			f.Display = "highlight"
		case it.Size <= FilePlainMaxBytes:
			f.Display = "plain"
		default:
			f.Display = "download"
			f.Truncated = true
		}
		if f.Display != "download" {
			f.Encoding, f.Content = "utf-8", string(data)
		}
	case "image":
		if it.Size <= ImageInlineMaxBytes {
			f.Display = "image"
			f.Encoding, f.Content = "base64", base64.StdEncoding.EncodeToString(data)
		} else {
			f.Display = "download"
		}
	default:
		f.Display = "download"
	}

	last, err := s.lastCommits(ctx, dir, commitSHA, []TreeItem{it})
	if err != nil {
		return nil, err
	}
	f.LastCommit = last[0]
	return f, nil
}

// classify riconosce il tipo dai primi byte: immagini dai magic number (SVG
// dal testo), binario se c'è un NUL o non è UTF-8 valido, altrimenti testo.
func classify(head []byte) (kind, mime string) {
	switch {
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image", "image/png"
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return "image", "image/jpeg"
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return "image", "image/gif"
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image", "image/webp"
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "binary", ""
	}
	// Un carattere multibyte tagliato in fondo alla testa non è un errore.
	h := head
	for i := 0; i < 3 && len(h) > 0 && !utf8.Valid(h); i++ {
		h = h[:len(h)-1]
	}
	if !utf8.Valid(h) {
		return "binary", ""
	}
	if isSVG(h) {
		return "image", "image/svg+xml"
	}
	return "text", ""
}

func isSVG(head []byte) bool {
	t := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	t = bytes.TrimLeft(t, " \t\r\n")
	if len(t) == 0 || t[0] != '<' {
		return false
	}
	return bytes.Contains(bytes.ToLower(t), []byte("<svg"))
}

var errHeadFull = errors.New("gitread: testa letta")

type headWriter struct {
	buf []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	room := h.max - len(h.buf)
	if len(p) >= room {
		h.buf = append(h.buf, p[:room]...)
		return room, errHeadFull
	}
	h.buf = append(h.buf, p...)
	return len(p), nil
}

// blobHead legge i primi n byte di un blob senza caricarlo tutto.
func (s *Service) blobHead(ctx context.Context, dir, blobSHA string, n int) ([]byte, error) {
	w := &headWriter{max: n}
	err := s.run.Stream(ctx, dir, w, "cat-file", "blob", blobSHA)
	if err != nil && len(w.buf) < w.max {
		return nil, err
	}
	return w.buf, nil
}

// readmeNames sono i nomi del README, in ordine di preferenza.
var readmeNames = []string{"readme.md", "readme", "readme.txt"}

// Readme torna il README della cartella p (radice se vuoto) a ref, cercato
// come README.md, README, README.txt senza distinguere le maiuscole, con le
// regole di File. ErrNotFound se la cartella non esiste o non ha un README.
func (s *Service) Readme(ctx context.Context, repoID, ref, dirPath string) (*File, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	p, err := gitref.ValidatePath(dirPath, false)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		return nil, err
	}
	treeSHA, err := s.treeAt(ctx, dir, sha, p)
	if err != nil {
		return nil, err
	}
	items, err := s.lsTree(ctx, dir, treeSHA, p, false)
	if err != nil {
		return nil, err
	}
	for _, want := range readmeNames {
		for _, it := range items {
			if it.Type == "blob" && strings.ToLower(path.Base(it.Path)) == want {
				return s.fileOf(ctx, dir, sha, ref, it)
			}
		}
	}
	return nil, ErrNotFound
}

// Raw è un file pronto da mandare in streaming (regola B3).
type Raw struct {
	SHA  string
	Size int64
	// ContentType: text/plain; charset=utf-8 per il testo (SVG escluso),
	// application/octet-stream per il resto.
	ContentType string
	// Filename è il nome sicuro per Content-Disposition.
	Filename string
	// Attachment è true per tutto ciò che non è testo semplice.
	Attachment bool

	svc *Service
	dir string
}

// PrepareRaw risolve ref e percorso e decide il Content-Type sicuro prima del
// primo byte, così gli errori escono come JSON. Mai text/html né
// image/svg+xml. ErrNotFound se p non esiste o non è un file.
func (s *Service) PrepareRaw(ctx context.Context, repoID, ref, p string) (*Raw, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	fp, err := gitref.ValidatePath(p, true)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		return nil, err
	}
	it, err := s.entryAt(ctx, dir, sha, fp)
	if err != nil {
		return nil, err
	}
	if it.Type != "blob" {
		return nil, ErrNotFound
	}
	head, err := s.blobHead(ctx, dir, it.SHA, sniffBytes)
	if err != nil {
		return nil, err
	}
	kind, _ := classify(head)
	r := &Raw{SHA: it.SHA, Size: it.Size, Filename: safeFilename(path.Base(fp)), svc: s, dir: dir}
	if kind == "text" {
		r.ContentType = "text/plain; charset=utf-8"
	} else {
		r.ContentType, r.Attachment = "application/octet-stream", true
	}
	return r, nil
}

// WriteTo manda i byte del file a w, in streaming.
func (r *Raw) WriteTo(ctx context.Context, w io.Writer) error {
	return r.svc.run.Stream(ctx, r.dir, w, "cat-file", "blob", r.SHA)
}

// safeFilename toglie da un nome ciò che romperebbe l'intestazione
// Content-Disposition (apici, backslash, controlli, non ASCII).
func safeFilename(n string) string {
	var b strings.Builder
	for _, c := range n {
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == '/' || c == ';' || c == '%' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(c)
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}
