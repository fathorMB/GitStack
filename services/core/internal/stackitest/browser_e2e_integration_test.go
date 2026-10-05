//go:build integration

package stackitest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Storia di prova del browser del codice (GIT-89). Tutto il resto del file
// la legge via API sullo stack completo: il repo è creato via API e riempito
// con un push HTTPS vero, non con un fetch dentro il bare.
const (
	browserPrivate = "quarzo-codice" // privato di alice
	browserOpen    = "quarzo-aperto" // interno di alice, stessa storia
	needleUnique   = "LORENZO-UNICO-5c2e"
)

// 1x1 PNG valido (immagine comune, sotto 1 MB).
const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

type bCommit struct {
	SHA     string   `json:"sha"`
	Subject string   `json:"subject"`
	Parents []string `json:"parents"`
	Author  struct {
		Name string `json:"name"`
		User *struct {
			Username string `json:"username"`
			Kind     string `json:"kind"`
		} `json:"user"`
	} `json:"author"`
}

type bFile struct {
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	Kind           string `json:"kind"`
	MimeType       string `json:"mimeType"`
	Display        string `json:"display"`
	Truncated      bool   `json:"truncated"`
	Encoding       string `json:"encoding"`
	Content        string `json:"content"`
	Name           string `json:"name"`
	LastCommit     bCommit
	LastCommitJSON json.RawMessage `json:"lastCommit"`
}

type bDiffFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Collapsed      bool   `json:"collapsed"`
	CollapseReason string `json:"collapseReason"`
	Patch          string `json:"patch"`
}

type bDetail struct {
	Commit       bCommit     `json:"commit"`
	Files        []bDiffFile `json:"files"`
	FilesChanged int         `json:"filesChanged"`
	Additions    int         `json:"additions"`
	Deletions    int         `json:"deletions"`
	ListOnly     bool        `json:"listOnly"`
	IgnoreWS     bool        `json:"ignoreWhitespace"`
}

// browserEnv è lo stack con il repo di prova già spinto.
type browserEnv struct {
	*gitEnv
	privID, openID string
	// sha dei commit chiave
	shaFirst, shaBot, shaMerge, shaBig, shaSpaces, shaBulk string
	allSHAs                                                []string
}

func (b *browserEnv) url(repo, path string) string {
	return b.gateway + "/v1/repos/alice/" + repo + path
}

func (b *browserEnv) get(repo, path string, hdr map[string]string) fullReply {
	b.t.Helper()
	return rawGet(b.t, b.url(repo, path), hdr)
}

func (b *browserEnv) ok(repo, path string, hdr map[string]string) fullReply {
	b.t.Helper()
	r := b.get(repo, path, hdr)
	if r.status != 200 {
		b.t.Fatalf("GET %s: %d %s", path, r.status, truncate(r.body))
	}
	return r
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

func decode[T any](t *testing.T, r fullReply) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("JSON: %v\n%s", err, truncate(r.body))
	}
	return v
}

func lines(prefix string, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s riga %07d lorem ipsum dolor sit amet\n", prefix, i)
	}
	return sb.String()
}

// pushStory prepara la storia e la spinge via HTTPS nei due repo.
func newBrowserEnv(t *testing.T) *browserEnv {
	t.Helper()
	e := newGitEnv(t)
	b := &browserEnv{gitEnv: e}
	b.privID = e.createRepo("alice", browserPrivate, "private")
	b.openID = e.createRepo("alice", browserOpen, "internal")
	// un agente: i suoi commit portano il badge
	login := e.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, login, 200, "")
	want(t, e.gw("POST", "/users", map[string]any{"username": "botty", "kind": "agent", "email": "botty@agents.example.com"}, nil, e.session(login)), 201, "")

	dir := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	g := func(args ...string) string { return strings.TrimSpace(e.mustGit(dir, "", args...)) }
	write := func(p, c string) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(author, msg string) string {
		g("add", "-A")
		g("commit", "-q", "--author", author, "-m", msg)
		sha := g("rev-parse", "HEAD")
		b.allSHAs = append(b.allSHAs, sha)
		return sha
	}
	const alice = "Alice <alice@example.com>"
	const bot = "Botty <botty@agents.example.com>"

	g("init", "-q", "-b", "main")
	png, _ := base64.StdEncoding.DecodeString(pngBase64)
	bin := make([]byte, 4096)
	for i := range bin {
		bin[i] = byte(i * 7) // contiene byte nulli: binario
	}
	write("README.md", "# Progetto Quarzo\n\nLeggi la [guida](docs/guida.md).\n\n![schema](assets/pixel.png)\n")
	write("main.go", "package main\n\n// "+needleUnique+"\nfunc main() {\n\tprintln(\"quarzo\")\n}\n"+strings.Repeat("// riempimento per pesare in byte\n", 60))
	write("docs/guida.md", "# Guida\n\nUna riga con "+needleUnique+" dentro.\n")
	write("index.html", "<html><body><script>alert('xss')</script></body></html>\n")
	write("logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert('xss')</script></svg>`+"\n")
	write("assets/pixel.png", string(png))
	write("blob.bin", string(bin))
	write("small.txt", "una\nriga\nsotto\n1 MB\n")
	write("media.txt", lines("m", 40000))   // ~1,6 MB: fascia 1–5 MB
	write("grande.txt", lines("g", 170000)) // ~6,8 MB: oltre 5 MB
	write(secretFile, secretContent+"\n")
	b.shaFirst = commit(alice, "primo commit")
	g("tag", "v0.1") // tag leggero

	write("main.go", "package main\n\n// "+needleUnique+"\nfunc main() {\n\tprintln(\"quarzo dal bot\")\n}\n"+strings.Repeat("// riempimento per pesare in byte\n", 60))
	b.shaBot = commit(bot, "modifica del bot")

	g("checkout", "-q", "-b", "feature/uno")
	write("feature.txt", "una funzione nuova\n")
	commit(alice, "aggiunge feature")
	g("checkout", "-q", "main")
	write("su-main.txt", "lavoro su main\n")
	commit(alice, "lavoro su main")
	g("merge", "-q", "--no-ff", "-m", "Merge feature/uno", "feature/uno")
	b.shaMerge = g("rev-parse", "HEAD")
	b.allSHAs = append(b.allSHAs, b.shaMerge)

	write("small.txt", "  una\n  riga\n  sotto\n  1 MB\n")
	b.shaSpaces = commit(alice, "solo spazi")

	// commit con un file oltre 500 righe e un package-lock.json
	write("big.txt", lines("b", 600))
	write("package-lock.json", "{\n  \"name\": \"quarzo\",\n  \"lockfileVersion\": 3\n}\n")
	write("notes.txt", "nota breve\n")
	b.shaBig = commit(alice, "file lungo e lock")
	g("tag", "-a", "v1.0", "-m", "release 1.0 del progetto quarzo")

	// branch con oltre 300 file in un solo commit (B6: solo elenco)
	g("checkout", "-q", "-b", "bulk/301")
	for i := 0; i < 301; i++ {
		write(fmt.Sprintf("bulk/f%03d.txt", i), fmt.Sprintf("file %d\n", i))
	}
	b.shaBulk = commit(alice, "301 file")
	g("checkout", "-q", "main")

	for _, repo := range []string{browserPrivate, browserOpen} {
		u := e.httpsURL("alice", e.tokens["alice"], "/alice/"+repo+".git")
		g("push", "-q", u, "main", "feature/uno", "bulk/301")
		g("push", "-q", u, "--tags")
	}
	return b
}

func TestBrowserCodice(t *testing.T) {
	b := newBrowserEnv(t)
	alice := b.bearer("alice")
	P := browserPrivate

	t.Run("albero", func(t *testing.T) {
		tree := decode[struct {
			CommitSha string `json:"commitSha"`
			Entries   []struct {
				Name       string  `json:"name"`
				Path       string  `json:"path"`
				Type       string  `json:"type"`
				LastCommit bCommit `json:"lastCommit"`
			} `json:"entries"`
			Truncated bool `json:"truncated"`
		}](t, b.ok(P, "/tree", alice))
		if tree.CommitSha == "" || tree.Truncated {
			t.Errorf("albero: %+v", tree)
		}
		var names []string
		seenFile := false
		for _, en := range tree.Entries {
			names = append(names, en.Name)
			if en.Type == "file" {
				seenFile = true
			} else if seenFile && en.Type == "dir" {
				t.Errorf("le cartelle vengono prima dei file: %v", names)
			}
			if en.LastCommit.SHA == "" || en.LastCommit.Subject == "" {
				t.Errorf("%s senza ultimo commit", en.Name)
			}
		}
		for _, n := range []string{"README.md", "docs", "assets", "grande.txt", "media.txt", "blob.bin", "index.html", "logo.svg", "package-lock.json", "big.txt"} {
			if !contains(names, n) {
				t.Errorf("albero senza %s: %v", n, names)
			}
		}
		sub := decode[struct {
			Entries []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"entries"`
		}](t, b.ok(P, "/tree?path=docs", alice))
		if len(sub.Entries) != 1 || sub.Entries[0].Path != "docs/guida.md" {
			t.Errorf("cartella docs: %+v", sub.Entries)
		}
		// al tag leggero v0.1 non esistono ancora né feature.txt né big.txt
		old := decode[struct {
			Entries []struct{ Name string } `json:"entries"`
		}](t, b.ok(P, "/tree?ref=v0.1", alice))
		for _, en := range old.Entries {
			if en.Name == "feature.txt" || en.Name == "big.txt" {
				t.Errorf("v0.1 contiene %s", en.Name)
			}
		}
		if r := b.get(P, "/tree?path=non-esiste", alice); r.status != 404 {
			t.Errorf("percorso inesistente: %d", r.status)
		}
		if r := b.get(P, "/tree?ref=non-esiste", alice); r.status != 404 {
			t.Errorf("ref inesistente: %d", r.status)
		}
		if r := b.get(P, "/tree?ref=a..b", alice); r.status != 400 {
			t.Errorf("ref non valido: %d", r.status)
		}
	})

	t.Run("file_B1_tre_fasce", func(t *testing.T) {
		small := decode[bFile](t, b.ok(P, "/contents?path=small.txt", alice))
		if small.Kind != "text" || small.Display != "highlight" || small.Truncated || !strings.Contains(small.Content, "riga") {
			t.Errorf("sotto 1 MB: %+v", small)
		}
		mid := decode[bFile](t, b.ok(P, "/contents?path=media.txt", alice))
		if mid.Size <= 1<<20 || mid.Size > 5<<20 {
			t.Fatalf("media.txt non è fra 1 e 5 MB: %d", mid.Size)
		}
		if mid.Display != "plain" || mid.Truncated || int64(len(mid.Content)) != mid.Size {
			t.Errorf("1–5 MB: display=%s truncated=%v content=%d size=%d", mid.Display, mid.Truncated, len(mid.Content), mid.Size)
		}
		big := decode[bFile](t, b.ok(P, "/contents?path=grande.txt", alice))
		if big.Size <= 5<<20 {
			t.Fatalf("grande.txt non supera 5 MB: %d", big.Size)
		}
		if big.Display != "download" || !big.Truncated || big.Content != "" {
			t.Errorf("oltre 5 MB: display=%s truncated=%v content=%d byte", big.Display, big.Truncated, len(big.Content))
		}
		// il raw invece porta tutto il file
		raw := b.ok(P, "/raw?path=grande.txt", alice)
		if int64(len(raw.body)) != big.Size {
			t.Errorf("raw di grande.txt: %d byte, atteso %d", len(raw.body), big.Size)
		}
		// immagine comune: mostrata come immagine, in base64
		img := decode[bFile](t, b.ok(P, "/contents?path=assets/pixel.png", alice))
		wantPNG, _ := base64.StdEncoding.DecodeString(pngBase64)
		got, _ := base64.StdEncoding.DecodeString(img.Content)
		if img.Kind != "image" || img.Display != "image" || img.Encoding != "base64" || img.MimeType != "image/png" || !bytes.Equal(got, wantPNG) {
			t.Errorf("png: %+v", img)
		}
		// SVG: solo come immagine, mai come testo da incorporare
		svg := decode[bFile](t, b.ok(P, "/contents?path=logo.svg", alice))
		if svg.Kind != "image" || svg.MimeType != "image/svg+xml" || svg.Display != "image" {
			t.Errorf("svg: %+v", svg)
		}
		// binario: nessun contenuto, solo dimensione e download
		bin := decode[bFile](t, b.ok(P, "/contents?path=blob.bin", alice))
		if bin.Kind != "binary" || bin.Display != "download" || bin.Content != "" || bin.Size != 4096 {
			t.Errorf("binario: %+v", bin)
		}
		// HTML: è testo, evidenziato, mai eseguito
		html := decode[bFile](t, b.ok(P, "/contents?path=index.html", alice))
		if html.Kind != "text" || html.Display != "highlight" {
			t.Errorf("html: %+v", html)
		}
		if r := b.get(P, "/contents?path=non-esiste.txt", alice); r.status != 404 {
			t.Errorf("file inesistente: %d", r.status)
		}
	})

	t.Run("readme", func(t *testing.T) {
		rd := decode[bFile](t, b.ok(P, "/readme", alice))
		if rd.Name != "README.md" || !strings.Contains(rd.Content, "(docs/guida.md)") || !strings.Contains(rd.Content, "![schema](assets/pixel.png)") {
			t.Errorf("readme: %+v", rd)
		}
		// il link relativo e l'immagine puntano a file che esistono davvero
		b.ok(P, "/contents?path=docs/guida.md", alice)
		b.ok(P, "/raw/main/assets/pixel.png", alice)
	})

	t.Run("raw_header_di_sicurezza", func(t *testing.T) {
		check := func(label string, r fullReply, wantBody string, attachment bool) {
			t.Helper()
			if r.status != 200 {
				t.Fatalf("%s: %d", label, r.status)
			}
			ct := strings.ToLower(r.header.Get("Content-Type"))
			if strings.Contains(ct, "html") || strings.Contains(ct, "svg") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") {
				t.Errorf("%s: Content-Type %q eseguibile", label, ct)
			}
			if r.header.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s: manca nosniff", label)
			}
			if r.header.Get("Content-Security-Policy") != "sandbox" {
				t.Errorf("%s: CSP %q", label, r.header.Get("Content-Security-Policy"))
			}
			isAtt := strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment")
			if attachment != isAtt {
				t.Errorf("%s: Content-Disposition %q, attachment atteso=%v", label, r.header.Get("Content-Disposition"), attachment)
			}
			if wantBody != "" && !strings.Contains(string(r.body), wantBody) {
				t.Errorf("%s: i byte non sono quelli del file", label)
			}
		}
		for _, form := range []string{"/raw?path=", "/raw/main/"} {
			check("html "+form, b.get(P, form+"index.html", alice), "<script>alert('xss')</script>", false)
			if ct := b.get(P, form+"index.html", alice).header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("html come %q, atteso text/plain", ct)
			}
			check("svg "+form, b.get(P, form+"logo.svg", alice), "<script>alert('xss')</script>", true)
			check("png "+form, b.get(P, form+"assets/pixel.png", alice), "", true)
			check("bin "+form, b.get(P, form+"blob.bin", alice), "", true)
		}
		// ref con '/' nell'indirizzo per forma a coda
		check("ref con slash", b.get(P, "/raw/feature/uno/feature.txt", alice), "una funzione nuova", false)
		check("tag", b.get(P, "/raw/v0.1/small.txt", alice), "sotto", false)
		if r := b.get(P, "/raw/main/non-esiste.txt", alice); r.status != 404 {
			t.Errorf("raw inesistente: %d", r.status)
		}
	})

	t.Run("archivi", func(t *testing.T) {
		zr := b.ok(P, "/archive?ref=main", alice)
		tr := b.ok(P, "/archive?ref=main&format=tar.gz", alice)
		for _, r := range []fullReply{zr, tr} {
			if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Content-Security-Policy") != "sandbox" ||
				!strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment") {
				t.Errorf("header archivio: %v", r.header)
			}
		}
		if !strings.Contains(zr.header.Get("Content-Disposition"), P+"-main.zip") || !strings.Contains(tr.header.Get("Content-Disposition"), P+"-main.tar.gz") {
			t.Errorf("nomi: %q %q", zr.header.Get("Content-Disposition"), tr.header.Get("Content-Disposition"))
		}
		zip1, zdata := readZip(t, zr.body)
		tar1, tdata := readTarGz(t, tr.body)
		if strings.Join(zip1, ",") != strings.Join(tar1, ",") {
			t.Errorf("ZIP e tar.gz hanno file diversi:\n%v\n%v", zip1, tar1)
		}
		for _, n := range []string{"README.md", "docs/guida.md", "grande.txt", "assets/pixel.png", "feature.txt"} {
			if !contains(zip1, n) {
				t.Errorf("l'archivio non contiene %s: %v", n, zip1)
			}
		}
		if !bytes.Equal(zdata["blob.bin"], tdata["blob.bin"]) || len(zdata["blob.bin"]) != 4096 {
			t.Errorf("blob.bin diverso fra ZIP e tar.gz")
		}
		// il tag: lo stato di v0.1, senza il lavoro successivo
		old, _ := readZip(t, b.ok(P, "/archive?ref=v0.1", alice).body)
		if contains(old, "feature.txt") || contains(old, "big.txt") || !contains(old, "README.md") {
			t.Errorf("archivio di v0.1: %v", old)
		}
		// ref con '/' diventa '-' nel nome
		r := b.ok(P, "/archive?ref=feature/uno", alice)
		if !strings.Contains(r.header.Get("Content-Disposition"), P+"-feature-uno.zip") {
			t.Errorf("nome con slash: %q", r.header.Get("Content-Disposition"))
		}
		// per commit
		b.ok(P, "/archive?ref="+b.shaFirst+"&format=tar.gz", alice)
	})

	t.Run("branch_e_tag", func(t *testing.T) {
		br := decode[struct {
			Items []struct {
				Name      string `json:"name"`
				IsDefault bool   `json:"isDefault"`
				Protected bool   `json:"protected"`
			} `json:"items"`
			Total int `json:"total"`
		}](t, b.ok(P, "/branches", alice))
		if br.Total != 3 || len(br.Items) != 3 || br.Items[0].Name != "main" || !br.Items[0].IsDefault || !br.Items[0].Protected {
			t.Errorf("branch: %+v", br)
		}
		var names []string
		for _, it := range br.Items {
			names = append(names, it.Name)
		}
		if !contains(names, "feature/uno") || !contains(names, "bulk/301") {
			t.Errorf("branch: %v", names)
		}
		tags := decode[struct {
			Items []struct {
				Name      string  `json:"name"`
				Annotated bool    `json:"annotated"`
				Message   string  `json:"message"`
				TaggedAt  string  `json:"taggedAt"`
				Commit    bCommit `json:"commit"`
				ZipURL    string  `json:"zipUrl"`
				TarGzURL  string  `json:"tarGzUrl"`
			} `json:"items"`
			Total int `json:"total"`
		}](t, b.ok(P, "/tags", alice))
		if tags.Total != 2 || len(tags.Items) != 2 {
			t.Fatalf("tag: %+v", tags)
		}
		for _, tg := range tags.Items {
			if tg.TaggedAt == "" || tg.Commit.SHA == "" || tg.ZipURL == "" || tg.TarGzURL == "" {
				t.Errorf("tag %s incompleto: %+v", tg.Name, tg)
			}
			switch tg.Name {
			case "v1.0":
				if !tg.Annotated || !strings.Contains(tg.Message, "release 1.0") || tg.Commit.SHA != b.shaBig {
					t.Errorf("tag annotato: %+v", tg)
				}
			case "v0.1":
				if tg.Annotated || tg.Commit.SHA != b.shaFirst {
					t.Errorf("tag leggero: %+v", tg)
				}
			default:
				t.Errorf("tag inatteso %s", tg.Name)
			}
			// gli indirizzi di download dei tag funzionano davvero
			dl := rawGet(t, b.gateway+"/v1"+tg.ZipURL, alice)
			if dl.status != 200 {
				t.Errorf("zipUrl %s: %d", tg.ZipURL, dl.status)
			}
		}
		// le regole di push: il default è protetto, gli altri no
		for _, it := range br.Items {
			if it.Name != "main" && it.Protected {
				t.Errorf("%s non dovrebbe essere protetto", it.Name)
			}
		}
	})

	t.Run("storico", func(t *testing.T) {
		list := decode[struct {
			Items   []bCommit `json:"items"`
			HasMore bool      `json:"hasMore"`
			Page    int       `json:"page"`
		}](t, b.ok(P, "/commits", alice))
		subjects := map[string]bCommit{}
		for _, c := range list.Items {
			subjects[c.Subject] = c
		}
		for _, s := range []string{"primo commit", "modifica del bot", "aggiunge feature", "lavoro su main", "Merge feature/uno", "solo spazi", "file lungo e lock"} {
			if _, ok := subjects[s]; !ok {
				t.Errorf("storico senza %q", s)
			}
		}
		if len(subjects["Merge feature/uno"].Parents) != 2 {
			t.Errorf("il merge ha %d genitori", len(subjects["Merge feature/uno"].Parents))
		}
		if u := subjects["modifica del bot"].Author.User; u == nil || u.Username != "botty" || u.Kind != "agent" {
			t.Errorf("autore agente: %+v", subjects["modifica del bot"].Author)
		}
		if u := subjects["primo commit"].Author.User; u == nil || u.Username != "alice" || u.Kind != "human" {
			t.Errorf("autore umano: %+v", subjects["primo commit"].Author)
		}
		// paginazione
		pg := decode[struct {
			Items   []bCommit `json:"items"`
			HasMore bool      `json:"hasMore"`
		}](t, b.ok(P, "/commits?perPage=2", alice))
		if len(pg.Items) != 2 || !pg.HasMore {
			t.Errorf("pagina: %d voci, hasMore=%v", len(pg.Items), pg.HasMore)
		}
		// storico del singolo file: main.go è toccato da due soli commit
		fh := decode[struct{ Items []bCommit }](t, b.ok(P, "/commits?path=main.go", alice))
		if len(fh.Items) != 2 || fh.Items[0].SHA != b.shaBot || fh.Items[1].SHA != b.shaFirst {
			t.Errorf("storico di main.go: %+v", fh.Items)
		}
		// per autore
		bots := decode[struct{ Items []bCommit }](t, b.ok(P, "/commits?author=botty@agents.example.com", alice))
		if len(bots.Items) != 1 || bots.Items[0].SHA != b.shaBot {
			t.Errorf("filtro autore: %+v", bots.Items)
		}
		// su un altro ref
		tg := decode[struct{ Items []bCommit }](t, b.ok(P, "/commits?ref=v0.1", alice))
		if len(tg.Items) != 1 || tg.Items[0].SHA != b.shaFirst {
			t.Errorf("storico a v0.1: %+v", tg.Items)
		}
	})

	t.Run("dettaglio_commit_B6", func(t *testing.T) {
		d := decode[bDetail](t, b.ok(P, "/commits/"+b.shaBig, alice))
		by := map[string]bDiffFile{}
		for _, f := range d.Files {
			by[f.Path] = f
		}
		if f := by["big.txt"]; !f.Collapsed || f.CollapseReason != "large" || f.Additions != 600 {
			t.Errorf("big.txt (oltre 500 righe) deve essere chiuso: %+v", f)
		}
		if f := by["package-lock.json"]; !f.Collapsed || f.CollapseReason != "lock" {
			t.Errorf("package-lock.json deve essere chiuso: %+v", f)
		}
		if f := by["notes.txt"]; f.Collapsed || !strings.Contains(f.Patch, "+nota breve") {
			t.Errorf("notes.txt deve essere aperto, col patch: %+v", f)
		}
		if d.ListOnly || d.FilesChanged != 3 {
			t.Errorf("dettaglio: listOnly=%v filesChanged=%d", d.ListOnly, d.FilesChanged)
		}
		// con path si restringe a un file
		one := decode[bDetail](t, b.ok(P, "/commits/"+b.shaBig+"?path=notes.txt", alice))
		if len(one.Files) != 1 || one.Files[0].Path != "notes.txt" {
			t.Errorf("path: %+v", one.Files)
		}
		// prefisso dello sha
		short := decode[bDetail](t, b.ok(P, "/commits/"+b.shaBig[:8], alice))
		if short.Commit.SHA != b.shaBig {
			t.Errorf("prefisso: %s", short.Commit.SHA)
		}
		// ignora spazi: la re-indentazione di small.txt sparisce dal diff
		ws := decode[bDetail](t, b.ok(P, "/commits/"+b.shaSpaces, alice))
		if ws.Additions == 0 {
			t.Errorf("senza -w il cambio di spazi è un diff: %+v", ws)
		}
		nows := decode[bDetail](t, b.ok(P, "/commits/"+b.shaSpaces+"?ignoreWhitespace=true", alice))
		if !nows.IgnoreWS || nows.Additions != 0 || nows.Deletions != 0 {
			t.Errorf("con -w non ci sono righe cambiate: %+v", nows)
		}
		// il merge si confronta col primo genitore
		m := decode[bDetail](t, b.ok(P, "/commits/"+b.shaMerge, alice))
		if len(m.Commit.Parents) != 2 || len(m.Files) == 0 {
			t.Errorf("merge: %+v", m)
		}
		// oltre 300 file: solo elenco, senza patch
		bulk := decode[bDetail](t, b.ok(P, "/commits/"+b.shaBulk, alice))
		if !bulk.ListOnly || bulk.FilesChanged != 301 {
			t.Errorf("301 file: listOnly=%v filesChanged=%d", bulk.ListOnly, bulk.FilesChanged)
		}
		for _, f := range bulk.Files {
			if f.Patch != "" {
				t.Errorf("listOnly ma %s ha il patch", f.Path)
				break
			}
		}
		// errori
		if r := b.get(P, "/commits/zzzzzzzz", alice); r.status != 400 {
			t.Errorf("sha non esadecimale: %d", r.status)
		}
		if r := b.get(P, "/commits/"+strings.Repeat("0", 40), alice); r.status != 404 {
			t.Errorf("sha inesistente: %d", r.status)
		}
	})

	t.Run("diff_e_patch_scaricabili", func(t *testing.T) {
		d := b.ok(P, "/commits/"+b.shaBig+"/patch?format=diff", alice)
		if !strings.Contains(string(d.body), "diff --git a/big.txt b/big.txt") || !strings.Contains(string(d.body), "diff --git a/package-lock.json") ||
			strings.Count(string(d.body), "\n+b riga") != 600 {
			t.Errorf(".diff incompleto (nessun file chiuso né troncato): %d byte", len(d.body))
		}
		if cd := d.header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, b.shaBig[:12]+".diff") {
			t.Errorf(".diff: Content-Disposition %q", cd)
		}
		p := b.ok(P, "/commits/"+b.shaBig+"/patch?format=patch", alice)
		if !strings.Contains(string(p.body), "Subject: [PATCH] file lungo e lock") {
			t.Errorf(".patch: %s", truncate(p.body))
		}
		if cd := p.header.Get("Content-Disposition"); !strings.Contains(cd, b.shaBig[:12]+".patch") {
			t.Errorf(".patch: Content-Disposition %q", cd)
		}
		for _, r := range []fullReply{d, p} {
			if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Content-Security-Policy") != "sandbox" {
				t.Errorf("header: %v", r.header)
			}
		}
		// il commit con 301 file: l'elenco è senza patch, il .diff li ha tutti
		bulk := b.ok(P, "/commits/"+b.shaBulk+"/patch?format=diff", alice)
		if n := strings.Count(string(bulk.body), "diff --git a/bulk/"); n != 301 {
			t.Errorf(".diff del commit con 301 file: %d file", n)
		}
	})

	t.Run("blame", func(t *testing.T) {
		bl := decode[struct {
			Ranges []struct {
				StartLine int     `json:"startLine"`
				EndLine   int     `json:"endLine"`
				Commit    bCommit `json:"commit"`
			} `json:"ranges"`
		}](t, b.ok(P, "/blame?path=main.go", alice))
		shas := map[string]bool{}
		agent := false
		covered := 0
		for _, rg := range bl.Ranges {
			shas[rg.Commit.SHA] = true
			covered += rg.EndLine - rg.StartLine + 1
			if rg.Commit.SHA == b.shaBot && rg.Commit.Author.User != nil && rg.Commit.Author.User.Kind == "agent" {
				agent = true
			}
		}
		if !shas[b.shaFirst] || !shas[b.shaBot] || !agent || covered != 66 {
			t.Errorf("blame: commit %v, agente=%v, righe coperte=%d", shas, agent, covered)
		}
		for _, p := range []string{"blob.bin", "grande.txt", "media.txt"} {
			r := b.get(P, "/blame?path="+p, alice)
			if r.status != 400 || !strings.Contains(string(r.body), "blame_unavailable") {
				t.Errorf("blame di %s: %d %s", p, r.status, truncate(r.body))
			}
		}
	})

	t.Run("ricerca_e_elenco_file", func(t *testing.T) {
		res := decode[struct {
			Results []struct {
				Path     string `json:"path"`
				Line     int    `json:"line"`
				Fragment string `json:"fragment"`
			} `json:"results"`
			LimitReached bool `json:"limitReached"`
			TimedOut     bool `json:"timedOut"`
		}](t, b.ok(P, "/search?q="+strings.ToLower(needleUnique), alice)) // senza distinguere maiuscole
		var paths []string
		for _, r := range res.Results {
			paths = append(paths, r.Path)
			if !strings.Contains(strings.ToLower(r.Fragment), strings.ToLower(needleUnique)) || r.Line < 1 {
				t.Errorf("risultato: %+v", r)
			}
		}
		sort.Strings(paths)
		if strings.Join(paths, ",") != "docs/guida.md,main.go" || res.LimitReached || res.TimedOut {
			t.Errorf("ricerca: %v limit=%v timeout=%v", paths, res.LimitReached, res.TimedOut)
		}
		// al tag v0.1 la ricerca vede lo stato di quel ref
		many := decode[struct {
			Results      []struct{ Path string } `json:"results"`
			LimitReached bool                    `json:"limitReached"`
		}](t, b.ok(P, "/search?q=lorem+ipsum", alice))
		if len(many.Results) != 100 || !many.LimitReached {
			t.Errorf("massimo 100 risultati: %d, limitReached=%v", len(many.Results), many.LimitReached)
		}
		// i binari non si cercano
		bin := decode[struct{ Results []struct{ Path string } }](t, b.ok(P, "/search?q=quarzo&ref=v0.1", alice))
		for _, r := range bin.Results {
			if r.Path == "blob.bin" || r.Path == "assets/pixel.png" {
				t.Errorf("la ricerca entra nel binario %s", r.Path)
			}
		}
		if r := b.get(P, "/search?q=a", alice); r.status != 400 {
			t.Errorf("q di un carattere: %d", r.status)
		}
		fl := decode[struct {
			Paths     []string `json:"paths"`
			Truncated bool     `json:"truncated"`
		}](t, b.ok(P, "/files", alice))
		for _, p := range []string{"docs/guida.md", "assets/pixel.png", "bulk/f000.txt"} {
			// bulk/ esiste solo sul branch bulk/301
			if p == "bulk/f000.txt" {
				if contains(fl.Paths, p) {
					t.Errorf("main non ha %s", p)
				}
				continue
			}
			if !contains(fl.Paths, p) {
				t.Errorf("Go to file senza %s", p)
			}
		}
		if fl.Truncated {
			t.Error("elenco file troncato")
		}
	})

	t.Run("lingue", func(t *testing.T) {
		r := b.get(P, "/languages", alice)
		if r.status != 200 {
			t.Fatalf("lingue: %d %s", r.status, truncate(r.body))
		}
		l := decode[struct {
			Languages []struct {
				Name    string  `json:"name"`
				Bytes   int64   `json:"bytes"`
				Percent float64 `json:"percent"`
			} `json:"languages"`
			TotalBytes int64 `json:"totalBytes"`
		}](t, r)
		if len(l.Languages) == 0 || l.Languages[0].Name != "Go" || l.TotalBytes == 0 {
			t.Fatalf("lingue: %+v", l)
		}
		sum := 0.0
		for i, x := range l.Languages {
			sum += x.Percent
			if i > 0 && x.Bytes > l.Languages[i-1].Bytes {
				t.Errorf("lingue non in ordine decrescente: %+v", l.Languages)
			}
		}
		if sum < 99 || sum > 101 {
			t.Errorf("le percentuali sommano %.2f", sum)
		}
	})

	t.Run("permessi", func(t *testing.T) {
		b.permissions(t)
	})

	t.Run("ui_smoke", func(t *testing.T) {
		b.uiSmoke(t)
	})
}

// uiSmoke lancia il controllo di fumo della UI (web/src/smoke, Vitest con
// Testing Library e jsdom: lo strumento del resto di web/) contro questo
// stack: le pagine 07, 08, 22 (blame), 09, 10, 23 (Tags) e i risultati di Search code si caricano sul repo di prova senza
// errori. Serve node con pnpm e le dipendenze di web/ (corepack pnpm install
// --frozen-lockfile): se mancano il sottotest è saltato, tranne con
// GITSTACK_REQUIRE_UI_SMOKE=1 (la CI), dove è un errore.
func (b *browserEnv) uiSmoke(t *testing.T) {
	skip := func(why string) {
		if os.Getenv("GITSTACK_REQUIRE_UI_SMOKE") != "" {
			t.Fatalf("smoke UI richiesto ma non eseguibile: %s", why)
		}
		t.Skip(why)
	}
	web, err := filepath.Abs("../../../../web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("corepack"); err != nil {
		skip("corepack non è nel PATH")
	}
	if _, err := os.Stat(filepath.Join(web, "node_modules")); err != nil {
		skip("web/node_modules manca: corepack pnpm install --frozen-lockfile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "corepack", "pnpm", "exec", "vitest", "run", "src/smoke")
	cmd.Dir = web
	cmd.Env = append(os.Environ(),
		"VITE_SMOKE_GATEWAY="+b.gateway,
		"VITE_SMOKE_TOKEN="+b.tokens["alice"],
		"VITE_SMOKE_REPO=alice/"+browserPrivate,
		"VITE_SMOKE_SHA="+b.shaBig,
		"VITE_SMOKE_NEEDLE="+needleUnique,
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke UI: %v\n%s", err, out)
	}
	plain := ansiRe.ReplaceAllString(string(out), "")
	if strings.Contains(plain, "skipped") || strings.Contains(plain, "failed") || !strings.Contains(plain, "passed") {
		t.Fatalf("smoke UI: test saltati, falliti o non eseguiti:\n%s", out)
	}
	t.Logf("smoke UI:\n%s", out)
}

// permissions: come GIT-84, su tutte le letture del browser. Ogni caso
// negativo controlla anche che il corpo non porti dati del repo.
func (b *browserEnv) permissions(t *testing.T) {
	alice, bob := b.bearer("alice"), b.bearer("bob")
	// token di alice senza lo scope di lettura delle risorse
	noRead := b.gw("POST", "/user/tokens", map[string]any{
		"name": "solo-utente", "scopes": []string{"read:user"},
		"expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}, nil, b.cookies["alice"])
	want(t, noRead, 201, "")
	tok, _ := noRead.json()["token"].(string)
	aliceNoRead := map[string]string{"Authorization": "Bearer " + tok}

	reads := []string{
		"/tree", "/tree?path=docs", "/contents?path=index.html", "/contents?path=grande.txt", "/readme",
		"/raw?path=index.html", "/raw?path=logo.svg", "/raw/main/index.html", "/raw/main/grande.txt", "/raw/feature/uno/feature.txt",
		"/branches", "/tags", "/commits", "/commits?path=main.go", "/commits/" + b.shaBot, "/commits/" + b.shaBig,
		"/blame?path=main.go", "/archive?ref=main", "/archive?ref=v1.0&format=tar.gz",
		"/commits/" + b.shaBig + "/patch?format=diff", "/commits/" + b.shaBig + "/patch?format=patch",
		"/files", "/search?q=" + strings.ToLower(needleUnique), "/languages",
	}
	needles := append([]string{browserPrivate, browserOpen, b.privID, b.openID, "Quarzo", "quarzo", needleUnique, "README.md", "guida.md", "feature/uno", "grande.txt", "botty", "alice@example.com", "release 1.0"}, b.allSHAs...)
	for _, s := range b.allSHAs {
		needles = append(needles, s[:7])
	}
	noData := func(label string, r fullReply) {
		t.Helper()
		body := string(r.body)
		for _, n := range needles {
			// il nome del repo nell'indirizzo non è nel corpo di un 404 generico
			if strings.Contains(body, n) {
				t.Errorf("%s: il corpo contiene %q: %s", label, n, truncate(r.body))
			}
		}
		leakCheck(t, label, body, b.shaFirst, b.shaBot, b.shaBig)
	}
	for _, p := range reads {
		priv, intl := browserPrivate, browserOpen
		if r := b.get(priv, p, alice); r.status != 200 {
			t.Fatalf("%s: proprietario %d %s", p, r.status, truncate(r.body))
		}
		// utente senza permesso, repo privato: 404 come un repo che non c'è
		den := b.get(priv, p, bob)
		if den.status != 404 {
			t.Errorf("%s: utente senza permesso %d, atteso 404", p, den.status)
		}
		noData(p+" bob/privato", den)
		miss := rawGet(t, b.gateway+"/v1/repos/alice/non-esiste"+p, bob)
		if miss.status != 404 || strings.ReplaceAll(string(den.body), browserPrivate, "") != strings.ReplaceAll(string(miss.body), "non-esiste", "") {
			t.Errorf("%s: 404 distinguibile da un repo inesistente: %q / %q", p, den.body, miss.body)
		}
		// senza credenziali: 401, ovunque (anche sul repo interno), raw e archivi compresi
		for _, repo := range []string{priv, intl} {
			un := b.get(repo, p, nil)
			if un.status != 401 {
				t.Errorf("%s (%s): senza credenziali %d, atteso 401", p, repo, un.status)
			}
			noData(p+" anonimo/"+repo, un)
			if un.header.Get("Content-Disposition") != "" {
				t.Errorf("%s: il 401 non deve essere un allegato", p)
			}
		}
		// token senza scope di lettura: 403
		if r := b.get(priv, p, aliceNoRead); r.status != 403 {
			t.Errorf("%s: token senza read:resource %d, atteso 403", p, r.status)
		} else {
			noData(p+" senza scope", r)
		}
		// repo interno: un altro utente con account lo legge
		if r := b.get(intl, p, bob); r.status != 200 {
			t.Errorf("%s: repo interno da bob %d %s", p, r.status, truncate(r.body))
		}
	}
	// raw e archivi con la sessione web (cookie) di alice: servono; un token revocato o inventato no
	for _, p := range []string{"/raw/main/index.html", "/archive?ref=main"} {
		r := rawGetCookie(t, b.url(browserPrivate, p), b.cookies["alice"])
		if r.status != 200 {
			t.Errorf("%s con la sessione: %d", p, r.status)
		}
		fake := b.get(browserPrivate, p, map[string]string{"Authorization": "Bearer gst_inventato"})
		if fake.status != 401 {
			t.Errorf("%s con token inventato: %d", p, fake.status)
		}
		noData(p+" token inventato", fake)
	}
	// il token di bob non scrive: l'API di lettura non ha rotte di modifica sul codice
	_ = bob
}

func rawGetCookie(t *testing.T, url string, c *http.Cookie) fullReply {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(c)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return fullReply{status: resp.StatusCode, header: resp.Header, body: b}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func readZip(t *testing.T, data []byte) ([]string, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ZIP non valido: %v", err)
	}
	var names []string
	files := map[string][]byte{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		n := stripTop(f.Name)
		names = append(names, n)
		files[n] = b
	}
	sort.Strings(names)
	return names, files
}

func readTarGz(t *testing.T, data []byte) ([]string, map[string][]byte) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip non valido: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar non valido: %v", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, _ := io.ReadAll(tr)
		n := stripTop(h.Name)
		names = append(names, n)
		files[n] = b
	}
	sort.Strings(names)
	return names, files
}

// stripTop toglie la cartella radice <repo>-<ref>/ degli archivi.
func stripTop(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}
