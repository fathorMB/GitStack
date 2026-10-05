package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// MaxBlameBytes è la dimensione massima di un file per il blame (B4).
const MaxBlameBytes = 1 << 20

// BlameRange è un intervallo di righe con il commit che le ha scritte per
// ultimo (BlameRange del contratto).
type BlameRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Commit    Commit `json:"commit"`
}

// Blame è il blame di un file (Blame del contratto).
type Blame struct {
	Ref    string       `json:"ref"`
	Path   string       `json:"path"`
	Ranges []BlameRange `json:"ranges"`
}

// Blame calcola il blame del file path a ref. ErrNotFound se il percorso non
// esiste o non è un file; ErrBlameUnavailable se è binario o supera 1 MB.
func (s *Service) Blame(ctx context.Context, repoID, ref, filePath string) (*Blame, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	p, err := gitref.ValidatePath(filePath, true)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		return nil, err
	}

	// Il file esiste ed è un blob? (`<sha>:<percorso>` non può essere
	// un'opzione: inizia con lo sha.)
	obj := sha + ":" + p
	typ, err := s.run.Output(ctx, dir, nil, "cat-file", "-t", obj)
	if err != nil {
		if isMissingObject(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if strings.TrimSpace(string(typ)) != "blob" {
		return nil, ErrNotFound
	}
	sz, err := s.run.Output(ctx, dir, nil, "cat-file", "-s", obj)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sz)), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("gitread: dimensione inattesa %q", sz)
	}
	if size > MaxBlameBytes {
		return nil, fmt.Errorf("%w: il file supera 1 MB", ErrBlameUnavailable)
	}
	content, err := s.run.Output(ctx, dir, nil, "cat-file", "blob", obj)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(content[:min(len(content), 8000)], 0) >= 0 {
		return nil, fmt.Errorf("%w: file binario", ErrBlameUnavailable)
	}

	out, err := s.run.Output(ctx, dir, nil, "blame", "--porcelain", sha, "--", p)
	if err != nil {
		return nil, err
	}
	lines, err := parsePorcelain(out)
	if err != nil {
		return nil, err
	}
	var order []string
	seen := map[string]bool{}
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			order = append(order, l)
		}
	}
	commits, err := s.commitsByID(ctx, dir, order)
	if err != nil {
		return nil, err
	}

	res := &Blame{Ref: ref, Path: p, Ranges: []BlameRange{}}
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[i] {
			j++
		}
		c, ok := commits[lines[i]]
		if !ok {
			return nil, fmt.Errorf("gitread: commit %s del blame non trovato", lines[i])
		}
		res.Ranges = append(res.Ranges, BlameRange{StartLine: i + 1, EndLine: j + 1, Commit: c})
		i = j + 1
	}
	return res, nil
}

func isMissingObject(err error) bool {
	var ge *gitrun.Error
	return errors.As(err, &ge) && ge.ExitCode == 128
}

// parsePorcelain torna, per ogni riga del file (indice = riga-1), lo sha del
// commit che l'ha scritta. Nel formato porcelain ogni riga ha un'intestazione
// `<sha> <riga originale> <riga finale> [<n>]`, poi (solo alla prima
// occorrenza di un commit) i suoi metadati, poi il contenuto preceduto da TAB.
func parsePorcelain(out []byte) ([]string, error) {
	var shas []string
	var cur string
	var final int
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		if line[0] == '\t' {
			if cur == "" {
				return nil, fmt.Errorf("gitread: contenuto senza intestazione nel blame")
			}
			for len(shas) < final {
				shas = append(shas, "")
			}
			shas[final-1] = cur
			cur = ""
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 3 && len(f[0]) >= 40 && gitref.IsHexSHA(f[0]) {
			if n, err := strconv.Atoi(f[2]); err == nil {
				cur, final = f[0], n
			}
		}
	}
	for i, s := range shas {
		if s == "" {
			return nil, fmt.Errorf("gitread: riga %d senza commit nel blame", i+1)
		}
	}
	return shas, nil
}
