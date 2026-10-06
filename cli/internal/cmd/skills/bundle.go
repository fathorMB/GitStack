package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/cli/internal/api"
)

// VersionFile è il file di versione scritto in ogni cartella installata: `update`
// ne confronta lo sha256 con quello di index.json.
const VersionFile = ".gs-skills-version"

const (
	maxIndexSize  = 1 << 20
	maxBundleSize = 32 << 20
	maxFileSize   = 4 << 20
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// meta è il contenuto di VersionFile.
type meta struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version,omitempty"`
	Host    string `json:"host,omitempty"`
}

// bundle è il pacchetto scaricato e verificato: nome della skill → file.
type bundle struct {
	Version string
	SHA256  string
	Names   []string
	Files   map[string]map[string][]byte
}

type index struct {
	Version string `json:"version"`
	Skills  struct {
		File   string `json:"file"`
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"skills"`
}

// hostBase è https://<host> (o lo schema esplicito dell'host).
func hostBase(host string) string {
	return strings.TrimSuffix(api.BaseURL(host), api.BasePath)
}

func get(ctx context.Context, hc *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("GET %s: risposta troppo grande", u)
	}
	return b, nil
}

// fetchBundle scarica index.json e gs-skills.zip, verifica lo sha256 e
// decomprime in memoria: se qualcosa non torna non si è scritto niente.
func fetchBundle(ctx context.Context, hc *http.Client, host string) (*bundle, error) {
	base := hostBase(host)
	raw, err := get(ctx, hc, base+"/downloads/index.json", maxIndexSize)
	if err != nil {
		return nil, err
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("index.json non valido: %w", err)
	}
	want := strings.ToLower(strings.TrimSpace(idx.Skills.SHA256))
	if want == "" || idx.Skills.URL == "" {
		return nil, errors.New("index.json non descrive le skills: istanza troppo vecchia?")
	}
	ref, err := url.Parse(idx.Skills.URL)
	if err != nil {
		return nil, fmt.Errorf("index.json: url delle skills non valido: %w", err)
	}
	baseURL, _ := url.Parse(base + "/downloads/")
	zipURL := baseURL.ResolveReference(ref).String()
	zb, err := get(ctx, hc, zipURL, maxBundleSize)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(zb)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("sha256 di %s non corrisponde a index.json (atteso %s, ottenuto %s): nessun file scritto", zipURL, want, got)
	}
	b, err := unzipSkills(zb)
	if err != nil {
		return nil, err
	}
	b.SHA256 = want
	b.Version = idx.Version
	return b, nil
}

func unzipSkills(data []byte) (*bundle, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("gs-skills.zip non valido: %w", err)
	}
	b := &bundle{Files: map[string]map[string][]byte{}}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := zf.Name
		if strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("gs-skills.zip: percorso non ammesso %q", name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") || clean != strings.TrimPrefix(name, "./") {
			return nil, fmt.Errorf("gs-skills.zip: percorso non ammesso %q", name)
		}
		parts := strings.SplitN(clean, "/", 2)
		if len(parts) < 2 {
			continue // README e LICENSE in radice: non sono skills
		}
		if !skillNameRe.MatchString(parts[0]) {
			return nil, fmt.Errorf("gs-skills.zip: nome di skill non valido %q", parts[0])
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxFileSize+1))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > maxFileSize {
			return nil, fmt.Errorf("gs-skills.zip: %q troppo grande", name)
		}
		if b.Files[parts[0]] == nil {
			b.Files[parts[0]] = map[string][]byte{}
		}
		b.Files[parts[0]][parts[1]] = body
	}
	for n, files := range b.Files {
		if _, ok := files["SKILL.md"]; !ok {
			return nil, fmt.Errorf("gs-skills.zip: la skill %q non ha SKILL.md", n)
		}
		b.Names = append(b.Names, n)
	}
	if len(b.Names) == 0 {
		return nil, errors.New("gs-skills.zip non contiene skills")
	}
	sort.Strings(b.Names)
	return b, nil
}

func readMeta(dir string) (meta, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, VersionFile))
	if err != nil {
		return meta{}, false
	}
	var m meta
	if json.Unmarshal(raw, &m) != nil || m.SHA256 == "" {
		return meta{}, false
	}
	return m, true
}

// installedSkills dà le skills di dir installate da gs (con VersionFile).
func installedSkills(dir string) map[string]meta {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]meta{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, ok := readMeta(filepath.Join(dir, e.Name())); ok {
			out[e.Name()] = m
		}
	}
	return out
}

// conflicts elenca le cartelle esistenti che non sono state installate da gs.
func conflicts(t target, b *bundle) []string {
	var out []string
	for _, n := range b.Names {
		d := filepath.Join(t.Dir, n)
		if _, err := os.Stat(d); err == nil {
			if _, ok := readMeta(d); !ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// writeSkills scrive le skills del pacchetto in t.Dir, sostituendo le cartelle
// esistenti. Ogni skill si prepara in una cartella temporanea e poi si rinomina.
func writeSkills(t target, b *bundle, host string) ([]string, error) {
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return nil, err
	}
	mj, err := json.Marshal(meta{SHA256: b.SHA256, Version: b.Version, Host: host})
	if err != nil {
		return nil, err
	}
	var written []string
	for _, n := range b.Names {
		tmp, err := os.MkdirTemp(t.Dir, ".gs-tmp-")
		if err != nil {
			return written, err
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			_ = os.RemoveAll(tmp)
			return written, err
		}
		fail := func(err error) ([]string, error) {
			_ = os.RemoveAll(tmp)
			return written, err
		}
		rels := make([]string, 0, len(b.Files[n]))
		for rel := range b.Files[n] {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for _, rel := range rels {
			p := filepath.Join(tmp, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return fail(err)
			}
			if err := os.WriteFile(p, b.Files[n][rel], 0o644); err != nil {
				return fail(err)
			}
		}
		if err := os.WriteFile(filepath.Join(tmp, VersionFile), append(mj, '\n'), 0o644); err != nil {
			return fail(err)
		}
		dst := filepath.Join(t.Dir, n)
		if err := os.RemoveAll(dst); err != nil {
			return fail(err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return fail(err)
		}
		written = append(written, dst)
	}
	return written, nil
}
