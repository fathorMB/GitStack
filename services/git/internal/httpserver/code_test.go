package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

const svgDoc = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`

func setupCode(t *testing.T) *env {
	t.Helper()
	e := setup(t)
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Reads: gitread.New(run, e.store.Dir)})
	if _, err = e.store.Create(context.Background(), rid, repostore.CreateOptions{
		Files:  []repostore.File{{Path: "README.md", Content: []byte("# demo\n")}},
		Author: repostore.Author{Name: "Ada", Email: "ada@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	// Il resto dei file (anche in sottocartelle) arriva con un push.
	bare, err := e.store.Dir(rid)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	for p, c := range map[string][]byte{"src/a.txt": []byte("ciao\n"), "logo.svg": []byte(svgDoc), "bin.dat": {0, 1, 2}} {
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, c, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"add", "-A"},
		{"-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "--quiet", "-m", "File"},
		{"fetch", "--quiet", bare, "main"},
		{"-c", "user.name=Ada", "-c", "user.email=ada@example.com", "merge", "--quiet", "--allow-unrelated-histories", "-m", "Unisci", "FETCH_HEAD"},
		{"push", "--quiet", bare, "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(gitrun.Env(), "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return e
}

func TestCodeReads_HTTP(t *testing.T) {
	e := setupCode(t)
	base := "/internal/git/repos/" + rid

	rec := e.do("GET", base+"/tree?ref=main", "", true)
	e.expect(rec, 200)
	var tree gitread.Tree
	if err := json.Unmarshal(rec.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Entries[0].Name != "src" || len(tree.Entries) != 4 || strings.Contains(rec.Body.String(), `"user"`) {
		t.Fatalf("albero: %s", rec.Body)
	}
	e.expect(e.do("GET", base+"/tree?ref=main&path=src", "", true), 200)

	rec = e.do("GET", base+"/contents?ref=main&path=src/a.txt", "", true)
	e.expect(rec, 200)
	var f gitread.File
	_ = json.Unmarshal(rec.Body.Bytes(), &f)
	if f.Content != "ciao\n" || f.Display != "highlight" {
		t.Fatalf("file: %s", rec.Body)
	}
	rec = e.do("GET", base+"/readme?ref=main", "", true)
	e.expect(rec, 200)
	if !strings.Contains(rec.Body.String(), `"name":"README.md"`) {
		t.Fatalf("readme: %s", rec.Body)
	}
	e.expect(e.do("GET", base+"/branches", "", true), 200)
	e.expect(e.do("GET", base+"/tags", "", true), 200)

	// Raw: testo in linea con gli header di B3; SVG e binari mai come pagina.
	rec = e.do("GET", base+"/raw?ref=main&path=src/a.txt", "", true)
	e.expect(rec, 200)
	h := rec.Header()
	if rec.Body.String() != "ciao\n" || h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "sandbox" || h.Get("Content-Length") != "5" || h.Get("Content-Disposition") != "" {
		t.Fatalf("raw testo: %v %q", h, rec.Body)
	}
	rec = e.do("GET", base+"/raw?ref=main&path=logo.svg", "", true)
	e.expect(rec, 200)
	h = rec.Header()
	if rec.Body.String() != svgDoc || h.Get("Content-Type") != "application/octet-stream" ||
		h.Get("Content-Disposition") != `attachment; filename="logo.svg"` || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("raw svg: %v", h)
	}
	rec = e.do("GET", base+"/raw?ref=main&path=bin.dat", "", true)
	e.expect(rec, 200)
	if !bytes.Equal(rec.Body.Bytes(), []byte{0, 1, 2}) || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("raw binario: %v", rec.Header())
	}

	// Archivi.
	rec = e.do("GET", base+"/archive?ref=main&name=demo", "", true)
	e.expect(rec, 200)
	h = rec.Header()
	if h.Get("Content-Type") != "application/zip" || h.Get("Content-Disposition") != `attachment; filename="demo-main.zip"` ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("archivio: %v", h)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	if !names["demo-main/src/a.txt"] || !names["demo-main/README.md"] {
		t.Fatalf("zip: %v", names)
	}
	rec = e.do("GET", base+"/archive?ref=main&name=demo&format=tar.gz", "", true)
	e.expect(rec, 200)
	if rec.Header().Get("Content-Type") != "application/gzip" || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `demo-main.tar.gz"`) {
		t.Fatalf("tar.gz: %v", rec.Header())
	}
}

func TestCodeReads_Errors(t *testing.T) {
	e := setupCode(t)
	base := "/internal/git/repos/" + rid
	cases := []struct {
		path string
		code int
		err  string
	}{
		{base + "/tree", 400, "invalid_ref"},
		{base + "/tree?ref=--output=/tmp/x", 400, "invalid_ref"},
		{base + "/tree?ref=nonesiste", 404, "ref_not_found"},
		{base + "/tree?ref=main&path=manca", 404, "not_found"},
		{base + "/tree?ref=main&path=../x", 400, "invalid_path"},
		{base + "/contents?ref=main", 400, "invalid_path"},
		{base + "/contents?ref=main&path=src", 404, "not_found"},
		{base + "/contents?ref=a..b&path=x", 400, "invalid_ref"},
		{base + "/readme?ref=main&path=src", 404, "not_found"},
		{base + "/raw?ref=nonesiste&path=README.md", 404, "ref_not_found"},
		{base + "/raw?ref=main&path=manca", 404, "not_found"},
		{base + "/raw?ref=-x&path=README.md", 400, "invalid_ref"},
		{base + "/archive?ref=main&name=demo&format=rar", 400, "invalid_request"},
		{base + "/archive?ref=main", 400, "invalid_request"},
		{base + "/archive?ref=nonesiste&name=demo", 404, "ref_not_found"},
		{base + "/archive?ref=%24%28id%29&name=demo", 404, "ref_not_found"},
		{"/internal/git/repos/0a1b2c3d-1111-4222-8333-000000000000/branches", 404, "not_found"},
	}
	for _, c := range cases {
		rec := e.do("GET", c.path, "", true)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.code || body.Error.Code != c.err {
			t.Errorf("%s: %d %s, voglio %d %s", c.path, rec.Code, rec.Body, c.code, c.err)
		}
	}
	if rec := e.do("GET", base+"/raw?ref=main&path=README.md", "", false); rec.Code != 401 {
		t.Fatalf("senza firma: %d", rec.Code)
	}
}
