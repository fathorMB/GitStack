package gitread

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

// Costanti delle letture del codice: le stesse di `x-code-read-limits` in
// api/openapi.yaml (fonte unica).
const (
	FileHighlightMaxBytes = 1048576
	FilePlainMaxBytes     = 5242880
	ImageInlineMaxBytes   = 1048576
	TreeMaxEntries        = 1000
)

// lastCommitWorkers è il numero di `git log -1` in parallelo per le voci di
// una cartella.
const lastCommitWorkers = 8

// TreeItem è una voce di `git ls-tree -l`. Size è -1 per ciò che non è un blob.
type TreeItem struct {
	Mode string `json:"mode"`
	Type string `json:"type"` // blob, tree, commit
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
	Path string `json:"path"` // dalla radice del repo
}

// ListTree elenca le voci dell'albero a ref (branch, tag o sha): solo i
// livelli sotto la cartella dir ("" = radice) oppure, con recursive, tutti i
// blob e i sottomodulo in profondità (come `git ls-tree -r -l`), ciascuno con
// modo, sha e dimensione in byte. È l'helper da usare per le letture che
// scorrono i file di un ref (lingue, elenco dei file). Gli errori sono quelli
// di gitref (400/404) e ErrNotFound se dir non è una cartella.
func (s *Service) ListTree(ctx context.Context, repoID, ref, dirPath string, recursive bool) ([]TreeItem, error) {
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
	return s.lsTree(ctx, dir, treeSHA, p, recursive)
}

// treeAt torna l'oggetto albero di p a sha ("" = la radice, il commit stesso).
func (s *Service) treeAt(ctx context.Context, dir, sha, p string) (string, error) {
	if p == "" {
		return sha, nil
	}
	it, err := s.entryAt(ctx, dir, sha, p)
	if err != nil {
		return "", err
	}
	if it.Type != "tree" {
		return "", ErrNotFound
	}
	return it.SHA, nil
}

// entryAt cerca la voce p (non vuoto) nell'albero di sha; ErrNotFound se manca.
func (s *Service) entryAt(ctx context.Context, dir, sha, p string) (*TreeItem, error) {
	out, err := s.run.Output(ctx, dir, nil, "ls-tree", "-z", "-l", sha, "--", p)
	if err != nil {
		return nil, err
	}
	items, err := parseLsTree(out, "")
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Path == p {
			return &items[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) lsTree(ctx context.Context, dir, treeish, prefix string, recursive bool) ([]TreeItem, error) {
	args := []string{"ls-tree", "-z", "-l"}
	if recursive {
		args = append(args, "-r")
	}
	out, err := s.run.Output(ctx, dir, nil, append(args, treeish)...)
	if err != nil {
		return nil, err
	}
	return parseLsTree(out, prefix)
}

// parseLsTree legge l'output `-z -l`: `<modo> <tipo> <sha> <size>\t<nome>\0`.
func parseLsTree(out []byte, prefix string) ([]TreeItem, error) {
	var items []TreeItem
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(rec), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, fmt.Errorf("gitread: voce di ls-tree inattesa: %q", rec)
		}
		size := int64(-1)
		if f[3] != "-" {
			n, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("gitread: dimensione in ls-tree: %w", err)
			}
			size = n
		}
		if prefix != "" {
			name = prefix + "/" + name
		}
		items = append(items, TreeItem{Mode: f[0], Type: f[1], SHA: f[2], Size: size, Path: name})
	}
	return items, nil
}

// TreeEntry è una voce di cartella (TreeEntry del contratto).
type TreeEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"` // file, dir, symlink, submodule
	Mode       string `json:"mode"`
	Size       *int64 `json:"size,omitempty"`
	LastCommit Commit `json:"lastCommit"`
}

// Tree è l'albero di una cartella (Tree del contratto).
type Tree struct {
	Ref       string      `json:"ref"`
	CommitSHA string      `json:"commitSha"`
	Path      string      `json:"path"`
	Entries   []TreeEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

func entryType(it TreeItem) string {
	switch {
	case it.Type == "tree":
		return "dir"
	case it.Type == "commit":
		return "submodule"
	case it.Mode == "120000":
		return "symlink"
	}
	return "file"
}

// Tree torna le voci della cartella p (radice se vuoto) a ref, con l'ultimo
// commit che ha toccato ciascuna: cartelle prima, poi il resto, per nome; al
// massimo TreeMaxEntries voci. ErrNotFound se p non esiste o non è una cartella.
func (s *Service) Tree(ctx context.Context, repoID, ref, dirPath string) (*Tree, error) {
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
	sort.SliceStable(items, func(i, j int) bool {
		di, dj := items[i].Type == "tree", items[j].Type == "tree"
		if di != dj {
			return di
		}
		return items[i].Path < items[j].Path
	})
	res := &Tree{Ref: ref, CommitSHA: sha, Path: p, Entries: []TreeEntry{}}
	if len(items) > TreeMaxEntries {
		items = items[:TreeMaxEntries]
		res.Truncated = true
	}
	last, err := s.lastCommits(ctx, dir, sha, items)
	if err != nil {
		return nil, err
	}
	for i, it := range items {
		e := TreeEntry{Name: it.Path[strings.LastIndex(it.Path, "/")+1:], Path: it.Path,
			Type: entryType(it), Mode: it.Mode, LastCommit: last[i]}
		if it.Type == "blob" {
			sz := it.Size
			e.Size = &sz
		}
		res.Entries = append(res.Entries, e)
	}
	return res, nil
}

// lastCommits trova per ogni voce l'ultimo commit che l'ha toccata,
// raggiungibile da sha (un `git log -1` per voce, in parallelo limitato).
func (s *Service) lastCommits(ctx context.Context, dir, sha string, items []TreeItem) ([]Commit, error) {
	shas := make([]string, len(items))
	errs := make([]error, len(items))
	sem := make(chan struct{}, lastCommitWorkers)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := s.run.Output(ctx, dir, nil, "log", "-1", "--format=%H", sha, "--", items[i].Path)
			if err != nil {
				errs[i] = err
				return
			}
			shas[i] = strings.TrimSpace(string(out))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	m, err := s.summaries(ctx, dir, shas)
	if err != nil {
		return nil, err
	}
	res := make([]Commit, len(items))
	for i, h := range shas {
		c, ok := m[h]
		if !ok {
			return nil, fmt.Errorf("gitread: ultimo commit di %q non trovato", items[i].Path)
		}
		res[i] = c
	}
	return res, nil
}

// summaries legge i commit (sha completi, anche ripetuti) come riepiloghi,
// senza il messaggio intero.
func (s *Service) summaries(ctx context.Context, dir string, shas []string) (map[string]Commit, error) {
	seen := map[string]bool{}
	var uniq []string
	for _, h := range shas {
		if h != "" && !seen[h] {
			seen[h] = true
			uniq = append(uniq, h)
		}
	}
	m, err := s.commitsByID(ctx, dir, uniq)
	if err != nil {
		return nil, err
	}
	for k, c := range m {
		c.Message = ""
		m[k] = c
	}
	return m, nil
}
