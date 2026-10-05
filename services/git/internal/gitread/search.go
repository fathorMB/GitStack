package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Costanti della regola B5: le stesse di `x-code-read-limits` in
// api/openapi.yaml (fonte unica).
const (
	FileListMaxPaths       = 50000
	SearchMaxResults       = 100
	SearchTimeout          = 10 * time.Second
	SearchFragmentMaxChars = 300
	SearchFileMaxBytes     = 1048576

	searchQueryMin = 2
	searchQueryMax = 256
)

// FileList è l'elenco dei file di un ref (FileList del contratto).
type FileList struct {
	Ref       string   `json:"ref"`
	CommitSHA string   `json:"commitSha"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

// CodeSearchHit è una corrispondenza (CodeSearchHit del contratto).
type CodeSearchHit struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Fragment string `json:"fragment"`
}

// CodeSearchResult è il risultato di una ricerca (CodeSearchResult del contratto).
type CodeSearchResult struct {
	Ref          string          `json:"ref"`
	Query        string          `json:"query"`
	Results      []CodeSearchHit `json:"results"`
	LimitReached bool            `json:"limitReached"`
	TimedOut     bool            `json:"timedOut"`
}

// SetSearchTimeout cambia il tempo massimo della ricerca (default
// SearchTimeout); serve ai test. d <= 0 ripristina il default.
func (s *Service) SetSearchTimeout(d time.Duration) { s.searchTimeout = d }

func (s *Service) searchTimeoutOrDefault() time.Duration {
	if s.searchTimeout > 0 {
		return s.searchTimeout
	}
	return SearchTimeout
}

// resolveOrEmpty risolve ref; in un repo senza branch né tag torna "" senza
// errore (repo vuoto). Un ref inesistente in un repo non vuoto resta
// gitref.ErrRefNotFound.
func (s *Service) resolveOrEmpty(ctx context.Context, dir, ref string) (string, error) {
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err == nil {
		return sha, nil
	}
	if errors.Is(err, gitref.ErrRefNotFound) {
		out, rerr := s.run.Output(ctx, dir, nil, "for-each-ref", "--count=1", "--format=%(refname)")
		if rerr != nil {
			return "", rerr
		}
		if len(out) == 0 {
			return "", nil
		}
	}
	return "", err
}

// Files torna i percorsi di tutti i file (non delle cartelle né dei
// sottomoduli) di ref, in ordine alfabetico, al massimo FileListMaxPaths.
func (s *Service) Files(ctx context.Context, repoID, ref string) (*FileList, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	sha, err := s.resolveOrEmpty(ctx, dir, ref)
	if err != nil {
		return nil, err
	}
	res := &FileList{Ref: ref, CommitSHA: sha, Paths: []string{}}
	if sha == "" {
		return res, nil
	}
	items, err := s.lsTree(ctx, dir, sha, "", true)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Type == "blob" {
			res.Paths = append(res.Paths, it.Path)
		}
	}
	sort.Strings(res.Paths)
	if len(res.Paths) > FileListMaxPaths {
		res.Paths = res.Paths[:FileListMaxPaths]
		res.Truncated = true
	}
	return res, nil
}

// SearchCode cerca q (sottostringa letterale, senza distinguere le maiuscole)
// nei file di testo fino a SearchFileMaxBytes di ref, con `git grep -F`: q
// arriva a git solo come valore di `-e`, mai come opzione né come regex. Al
// massimo SearchMaxResults risultati (LimitReached se ce ne sono altri); allo
// scadere del tempo massimo i risultati sono parziali e TimedOut è true.
func (s *Service) SearchCode(ctx context.Context, repoID, ref, q string) (*CodeSearchResult, error) {
	if n := utf8.RuneCountInString(q); n < searchQueryMin || n > searchQueryMax || !utf8.ValidString(q) ||
		strings.ContainsAny(q, "\x00\r\n") {
		return nil, fmt.Errorf("%w: q deve avere da 2 a 256 caratteri, su una riga", ErrInvalidInput)
	}
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	sha, err := s.resolveOrEmpty(ctx, dir, ref)
	if err != nil {
		return nil, err
	}
	res := &CodeSearchResult{Ref: ref, Query: q, Results: []CodeSearchHit{}}
	if sha == "" {
		return res, nil
	}
	// git grep non conosce la dimensione dei file: quelli oltre il limite si
	// scartano dai risultati.
	items, err := s.lsTree(ctx, dir, sha, "", true)
	if err != nil {
		return nil, err
	}
	big := map[string]bool{}
	for _, it := range items {
		if it.Type == "blob" && it.Size > SearchFileMaxBytes {
			big[it.Path] = true
		}
	}

	sctx, cancel := context.WithTimeout(ctx, s.searchTimeoutOrDefault())
	defer cancel()
	w := &grepWriter{prefix: sha + ":", q: q, big: big, res: res}
	err = s.run.Stream(sctx, dir, w,
		"grep", "-z", "-n", "-I", "-F", "-i", "--no-color", "-e", q, sha, "--")
	switch {
	case w.full:
		// Interrotto da noi dopo il 101° risultato.
	case err == nil:
	case isTimeout(sctx, err):
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res.TimedOut = true
	default:
		var ge *gitrun.Error
		// Uscita 1 senza messaggi: nessuna corrispondenza.
		if !(errors.As(err, &ge) && ge.ExitCode == 1 && ge.Stderr == "") {
			return nil, err
		}
	}
	if w.perr != nil {
		return nil, w.perr
	}
	return res, nil
}

func isTimeout(sctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || sctx.Err() != nil
}

var errSearchFull = errors.New("gitread: ricerca piena")

// grepWriter legge l'output di `git grep -z -n` (`<sha>:<percorso>\0<riga>\0
// <testo>\n`) e riempie res; dopo il 101° risultato ferma il comando.
type grepWriter struct {
	prefix string
	q      string
	big    map[string]bool
	res    *CodeSearchResult
	buf    []byte
	full   bool
	perr   error
}

func (g *grepWriter) Write(p []byte) (int, error) {
	if g.full {
		return 0, errSearchFull
	}
	g.buf = append(g.buf, p...)
	for {
		i1 := bytes.IndexByte(g.buf, 0)
		if i1 < 0 {
			break
		}
		i2 := bytes.IndexByte(g.buf[i1+1:], 0)
		if i2 < 0 {
			break
		}
		i2 += i1 + 1
		nl := bytes.IndexByte(g.buf[i2+1:], '\n')
		if nl < 0 {
			break
		}
		nl += i2 + 1
		path := strings.TrimPrefix(string(g.buf[:i1]), g.prefix)
		line, err := strconv.Atoi(string(g.buf[i1+1 : i2]))
		if err != nil {
			g.perr = fmt.Errorf("gitread: riga di git grep inattesa: %w", err)
			return 0, g.perr
		}
		text := g.buf[i2+1 : nl]
		g.buf = g.buf[nl+1:]
		if g.big[path] {
			continue
		}
		if len(g.res.Results) >= SearchMaxResults {
			g.res.LimitReached = true
			g.full = true
			return 0, errSearchFull
		}
		g.res.Results = append(g.res.Results, CodeSearchHit{Path: path, Line: line, Fragment: fragment(text, g.q)})
	}
	return len(p), nil
}

// fragment torna la riga tagliata a SearchFragmentMaxChars caratteri attorno
// alla prima occorrenza di q.
func fragment(text []byte, q string) string {
	s := strings.TrimRight(strings.ToValidUTF8(string(text), "�"), "\r")
	r := []rune(s)
	if len(r) <= SearchFragmentMaxChars {
		return s
	}
	pos := 0
	if i := strings.Index(strings.ToLower(s), strings.ToLower(q)); i >= 0 {
		pos = utf8.RuneCountInString(s[:i])
	}
	start := pos - (SearchFragmentMaxChars-utf8.RuneCountInString(q))/2
	if start < 0 {
		start = 0
	}
	if start+SearchFragmentMaxChars > len(r) {
		start = len(r) - SearchFragmentMaxChars
	}
	return string(r[start : start+SearchFragmentMaxChars])
}
