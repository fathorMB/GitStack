package gitread

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1, 2, 3, 0}, 20)...)

const svgText = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`

// codeRepo: radice con README.md, src/ (main.go, util/x.txt), testi di varie
// dimensioni, binario, PNG, SVG; poi un secondo commit che tocca solo src/main.go.
func codeRepo(t *testing.T) (*fixture, string, string) {
	f := newFixture(t)
	f.write("README.md", []byte("# demo\n"))
	f.write("src/main.go", []byte("package main\n"))
	f.write("src/util/x.txt", []byte("x\n"))
	f.write("small.txt", []byte("piccolo\n"))
	f.write("mid.txt", bytes.Repeat([]byte("a"), FileHighlightMaxBytes))     // esattamente 1 MB
	f.write("plain.txt", bytes.Repeat([]byte("b"), FileHighlightMaxBytes+1)) // 1 MB + 1
	f.write("edge5.txt", bytes.Repeat([]byte("c"), FilePlainMaxBytes))       // esattamente 5 MB
	f.write("huge.txt", bytes.Repeat([]byte("d"), FilePlainMaxBytes+1))
	f.write("bin.dat", []byte{0, 1, 2, 0, 255})
	f.write("latin1.txt", []byte{'c', 'a', 'f', 0xe9, '\n'})
	f.write("pic.png", pngBytes)
	f.write("bigpic.png", append(append([]byte{}, pngBytes...), make([]byte, ImageInlineMaxBytes)...))
	f.write("logo.svg", []byte(svgText))
	f.write("note;100%.txt", []byte("n\n"))
	c1 := f.commit(alice, "Primo")
	f.write("src/main.go", []byte("package main\n// v2\n"))
	c2 := f.commit(bob, "Secondo")
	return f, c1, c2
}

func TestTree_RootAndSubdir(t *testing.T) {
	f, c1, c2 := codeRepo(t)
	ctx := context.Background()
	root, err := f.svc.Tree(ctx, "r1", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if root.CommitSHA != c2 || root.Truncated || root.Path != "" {
		t.Fatalf("radice: %+v", root)
	}
	if root.Entries[0].Name != "src" || root.Entries[0].Type != "dir" || root.Entries[0].Size != nil {
		t.Fatalf("la cartella viene prima: %+v", root.Entries[0])
	}
	if root.Entries[0].LastCommit.SHA != c2 || root.Entries[0].LastCommit.Author.Email != "bob@example.com" {
		t.Fatalf("ultimo commit di src: %+v", root.Entries[0].LastCommit)
	}
	var readme *TreeEntry
	for i, e := range root.Entries {
		if e.Name == "README.md" {
			readme = &root.Entries[i]
		}
		if i > 0 && root.Entries[i-1].Type != "dir" && e.Type == "dir" {
			t.Fatalf("cartelle dopo i file: %+v", root.Entries)
		}
	}
	if readme == nil || readme.Type != "file" || readme.Mode != "100644" || readme.Size == nil || *readme.Size != 7 || readme.LastCommit.SHA != c1 {
		t.Fatalf("README: %+v", readme)
	}

	sub, err := f.svc.Tree(ctx, "r1", "main", "src/")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Path != "src" || len(sub.Entries) != 2 || sub.Entries[0].Path != "src/util" || sub.Entries[1].Path != "src/main.go" ||
		sub.Entries[0].Name != "util" || sub.Entries[0].LastCommit.SHA != c1 || sub.Entries[1].LastCommit.SHA != c2 {
		t.Fatalf("src: %+v", sub)
	}
	// Anche con lo sha corto.
	if _, err := f.svc.Tree(ctx, "r1", c1[:8], "src"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"nonesiste", "README.md"} {
		if _, err := f.svc.Tree(ctx, "r1", "main", p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestFile_B1(t *testing.T) {
	f, _, c2 := codeRepo(t)
	ctx := context.Background()
	get := func(p string) *File {
		t.Helper()
		r, err := f.svc.File(ctx, "r1", "main", p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return r
	}

	s := get("small.txt")
	if s.Display != "highlight" || s.Content != "piccolo\n" || s.Encoding != "utf-8" || s.Kind != "text" || s.Binary || s.Truncated || s.Size != 8 {
		t.Fatalf("piccolo: %+v", s)
	}
	m := get("mid.txt")
	if m.Display != "highlight" || len(m.Content) != FileHighlightMaxBytes {
		t.Fatalf("1 MB: %s %d", m.Display, len(m.Content))
	}
	p := get("plain.txt")
	if p.Display != "plain" || len(p.Content) != FileHighlightMaxBytes+1 || p.Truncated || p.Kind != "text" {
		t.Fatalf("1-5 MB: %s %d", p.Display, len(p.Content))
	}
	e := get("edge5.txt")
	if e.Display != "plain" || len(e.Content) != FilePlainMaxBytes {
		t.Fatalf("5 MB: %s %d", e.Display, len(e.Content))
	}
	h := get("huge.txt")
	if h.Display != "download" || h.Content != "" || h.Encoding != "" || !h.Truncated || h.Size != FilePlainMaxBytes+1 || h.Kind != "text" {
		t.Fatalf("oltre 5 MB: %+v", h)
	}

	b := get("bin.dat")
	if b.Kind != "binary" || !b.Binary || b.Display != "download" || b.Content != "" || b.Truncated {
		t.Fatalf("binario: %+v", b)
	}
	l := get("latin1.txt")
	if l.Kind != "binary" || l.Display != "download" {
		t.Fatalf("non UTF-8: %+v", l)
	}

	img := get("pic.png")
	if img.Kind != "image" || img.MimeType != "image/png" || !img.Binary || img.Display != "image" || img.Encoding != "base64" {
		t.Fatalf("png: %+v", img)
	}
	if dec, err := base64.StdEncoding.DecodeString(img.Content); err != nil || !bytes.Equal(dec, pngBytes) {
		t.Fatalf("contenuto png: %v", err)
	}
	big := get("bigpic.png")
	if big.Kind != "image" || big.Display != "download" || big.Content != "" || big.Truncated {
		t.Fatalf("png grande: %+v", big)
	}
	svg := get("logo.svg")
	if svg.Kind != "image" || svg.MimeType != "image/svg+xml" || svg.Display != "image" || !svg.Binary {
		t.Fatalf("svg: %+v", svg)
	}
	if svg.LastCommit.SHA == "" || svg.SHA == "" || svg.Name != "logo.svg" {
		t.Fatalf("metadati: %+v", svg)
	}
	if mg := get("src/main.go"); mg.LastCommit.SHA != c2 || mg.Path != "src/main.go" || mg.Name != "main.go" {
		t.Fatalf("main.go: %+v", mg)
	}

	for _, p := range []string{"src", "nonesiste.txt", "src/nonesiste"} {
		if _, err := f.svc.File(ctx, "r1", "main", p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestRaw_SafeTypes(t *testing.T) {
	f, _, _ := codeRepo(t)
	ctx := context.Background()
	cases := []struct {
		path, ctype string
		attach      bool
		want        []byte
	}{
		{"small.txt", "text/plain; charset=utf-8", false, []byte("piccolo\n")},
		{"logo.svg", "application/octet-stream", true, []byte(svgText)},
		{"pic.png", "application/octet-stream", true, pngBytes},
		{"bin.dat", "application/octet-stream", true, []byte{0, 1, 2, 0, 255}},
		{"huge.txt", "text/plain; charset=utf-8", false, bytes.Repeat([]byte("d"), FilePlainMaxBytes+1)},
		{"note;100%.txt", "text/plain; charset=utf-8", false, []byte("n\n")},
	}
	for _, c := range cases {
		r, err := f.svc.PrepareRaw(ctx, "r1", "main", c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		var buf bytes.Buffer
		if err := r.WriteTo(ctx, &buf); err != nil {
			t.Fatal(err)
		}
		if r.ContentType != c.ctype || r.Attachment != c.attach || !bytes.Equal(buf.Bytes(), c.want) || r.Size != int64(len(c.want)) {
			t.Errorf("%s: %q attach=%v size=%d", c.path, r.ContentType, r.Attachment, r.Size)
		}
		if strings.ContainsAny(r.Filename, "\"\\/") {
			t.Errorf("nome non sicuro: %q", r.Filename)
		}
	}
	if _, err := f.svc.PrepareRaw(ctx, "r1", "main", "src"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cartella: %v", err)
	}
}

func TestBranchesAndTags(t *testing.T) {
	f := newFixture(t)
	f.write("a.txt", []byte("1\n"))
	c1 := f.commit(alice, "Uno")
	f.git(alice, "tag", "light")
	f.write("a.txt", []byte("2\n"))
	c2 := f.commit(bob, "Due")
	f.git(bob, "tag", "-a", "v1", "-m", "Prima release\n\nNote.", c2)
	f.git(alice, "branch", "feature", c1)
	f.git(alice, "branch", "-M", "trunk")
	f.git(alice, "symbolic-ref", "HEAD", "refs/heads/trunk")
	ctx := context.Background()

	bl, err := f.svc.Branches(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if bl.Total != 2 || bl.Items[0].Name != "trunk" || !bl.Items[0].IsDefault || bl.Items[0].Commit.SHA != c2 ||
		bl.Items[1].Name != "feature" || bl.Items[1].IsDefault || bl.Items[1].Commit.SHA != c1 || bl.Items[0].Protected {
		t.Fatalf("branch: %+v", bl)
	}

	tl, err := f.svc.Tags(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if tl.Total != 2 {
		t.Fatalf("tag: %+v", tl)
	}
	ann, light := tl.Items[0], tl.Items[1]
	if ann.Name != "v1" || !ann.Annotated || !strings.HasPrefix(ann.Message, "Prima release") || ann.Commit.SHA != c2 || ann.TaggedAt.IsZero() {
		t.Fatalf("annotato: %+v", ann)
	}
	if light.Name != "light" || light.Annotated || light.Message != "" || light.Commit.SHA != c1 || !light.TaggedAt.Equal(light.Commit.Committer.Date) {
		t.Fatalf("leggero: %+v", light)
	}
	// Un tag si risolve come ref.
	if _, err := f.svc.Tree(ctx, "r1", "v1", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBranchesTags_EmptyRepo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bl, err := f.svc.Branches(ctx, "r1")
	if err != nil || bl.Total != 0 || bl.Items == nil {
		t.Fatalf("branch: %+v %v", bl, err)
	}
	tl, err := f.svc.Tags(ctx, "r1")
	if err != nil || tl.Total != 0 || tl.Items == nil {
		t.Fatalf("tag: %+v %v", tl, err)
	}
}

func TestReadme(t *testing.T) {
	f := newFixture(t)
	f.write("README.md", []byte("# radice\n"))
	f.write("docs/readme.TXT", []byte("docs\n"))
	f.write("docs/README", []byte("senza estensione\n"))
	f.write("vuota/x", []byte("x"))
	f.commit(alice, "c")
	ctx := context.Background()
	r, err := f.svc.Readme(ctx, "r1", "main", "")
	if err != nil || r.Name != "README.md" || r.Content != "# radice\n" || r.Display != "highlight" {
		t.Fatalf("radice: %+v %v", r, err)
	}
	d, err := f.svc.Readme(ctx, "r1", "main", "docs")
	if err != nil || d.Path != "docs/README" {
		t.Fatalf("docs: %+v %v", d, err)
	}
	if _, err := f.svc.Readme(ctx, "r1", "main", "vuota"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("senza README: %v", err)
	}
	if _, err := f.svc.Readme(ctx, "r1", "main", "manca"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cartella mancante: %v", err)
	}
}

func TestArchives_SameTree(t *testing.T) {
	f, c1, _ := codeRepo(t)
	f.git(alice, "tag", "v0", c1)
	ctx := context.Background()

	for _, ref := range []string{"main", "v0", c1} {
		expected := map[string]string{} // percorso -> sha del blob
		for _, line := range strings.Split(f.git(alice, "ls-tree", "-r", "-z", ref), "\x00") {
			meta, name, ok := strings.Cut(line, "\t")
			if ok {
				expected[name] = strings.Fields(meta)[2]
			}
		}
		for _, format := range []string{"zip", "tar.gz"} {
			a, err := f.svc.PrepareArchive(ctx, "r1", ref, format, "demo")
			if err != nil {
				t.Fatalf("%s %s: %v", ref, format, err)
			}
			var buf bytes.Buffer
			if err := a.WriteTo(ctx, &buf); err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{}
			if format == "zip" {
				zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
				if err != nil {
					t.Fatal(err)
				}
				for _, zf := range zr.File {
					if strings.HasSuffix(zf.Name, "/") {
						continue
					}
					rc, _ := zf.Open()
					b, _ := io.ReadAll(rc)
					_ = rc.Close()
					got[zf.Name] = b
				}
				if a.ContentType != "application/zip" {
					t.Errorf("tipo: %s", a.ContentType)
				}
			} else {
				gz, err := gzip.NewReader(&buf)
				if err != nil {
					t.Fatal(err)
				}
				tr := tar.NewReader(gz)
				for {
					h, err := tr.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if h.Typeflag == tar.TypeReg {
						b, _ := io.ReadAll(tr)
						got[h.Name] = b
					}
				}
				if a.ContentType != "application/gzip" {
					t.Errorf("tipo: %s", a.ContentType)
				}
			}
			if !strings.HasSuffix(a.Filename, "."+format) || !strings.HasPrefix(a.Filename, "demo-") || strings.ContainsAny(a.Filename, "/\"") {
				t.Errorf("nome file: %q", a.Filename)
			}
			prefix := strings.TrimSuffix(a.Filename, "."+format) + "/"
			if len(got) != len(expected) {
				t.Errorf("%s %s: %d file, voglio %d", ref, format, len(got), len(expected))
			}
			for name, blob := range expected {
				b, ok := got[prefix+name]
				if !ok {
					t.Errorf("%s %s: manca %q", ref, format, name)
					continue
				}
				if sum := blobSHA(b); sum != blob {
					t.Errorf("%s %s: %q diverso", ref, format, name)
				}
			}
		}
	}
}

func blobSHA(b []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d%c", len(b), 0)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func TestInvalidAndMissingRefs(t *testing.T) {
	f, _, _ := codeRepo(t)
	ctx := context.Background()
	bad := []string{"", "--all", "-x", "a..b", "a b", "a\nb", "x:y", "HEAD~1", "@{u}", "ref^"}
	for _, ref := range bad {
		if _, err := f.svc.Tree(ctx, "r1", ref, ""); !errors.Is(err, gitref.ErrInvalidRef) {
			t.Errorf("Tree %q: %v", ref, err)
		}
		if _, err := f.svc.File(ctx, "r1", ref, "small.txt"); !errors.Is(err, gitref.ErrInvalidRef) {
			t.Errorf("File %q: %v", ref, err)
		}
		if _, err := f.svc.PrepareRaw(ctx, "r1", ref, "small.txt"); !errors.Is(err, gitref.ErrInvalidRef) {
			t.Errorf("Raw %q: %v", ref, err)
		}
		if _, err := f.svc.PrepareArchive(ctx, "r1", ref, "zip", "demo"); !errors.Is(err, gitref.ErrInvalidRef) {
			t.Errorf("Archive %q: %v", ref, err)
		}
		if _, err := f.svc.Readme(ctx, "r1", ref, ""); !errors.Is(err, gitref.ErrInvalidRef) {
			t.Errorf("Readme %q: %v", ref, err)
		}
	}
	for _, ref := range []string{"nonesiste", "main;ls", "$(id)", "deadbeef", strings.Repeat("a", 40)} {
		if _, err := f.svc.Tree(ctx, "r1", ref, ""); !errors.Is(err, gitref.ErrRefNotFound) {
			t.Errorf("Tree %q: %v", ref, err)
		}
		if _, err := f.svc.PrepareArchive(ctx, "r1", ref, "zip", "demo"); !errors.Is(err, gitref.ErrRefNotFound) {
			t.Errorf("Archive %q: %v", ref, err)
		}
	}
	for _, p := range []string{"", "../x", "/abs", "a/../b", "a//b"} {
		if _, err := f.svc.File(ctx, "r1", "main", p); !errors.Is(err, gitref.ErrInvalidPath) {
			t.Errorf("File path %q: %v", p, err)
		}
	}
	if _, err := f.svc.PrepareArchive(ctx, "r1", "main", "rar", "demo"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("formato: %v", err)
	}
	if _, err := f.svc.PrepareArchive(ctx, "r1", "main", "zip", "../x"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nome: %v", err)
	}
}

func TestListTree_Recursive(t *testing.T) {
	f, _, _ := codeRepo(t)
	items, err := f.svc.ListTree(context.Background(), "r1", "main", "src", true)
	if err != nil || len(items) != 2 {
		t.Fatalf("%+v %v", items, err)
	}
	if items[0].Path != "src/main.go" || items[0].Type != "blob" || items[0].Size != 19 || items[1].Path != "src/util/x.txt" {
		t.Fatalf("%+v", items)
	}
}
