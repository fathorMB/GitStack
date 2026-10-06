package gitread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Branch è un branch (Branch del contratto). Protected resta false: la
// protezione (R9) la imposta core.
type Branch struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Protected bool   `json:"protected"`
	Commit    Commit `json:"commit"`
}

// BranchList è l'elenco dei branch.
type BranchList struct {
	Items []Branch `json:"items"`
	Total int      `json:"total"`
}

// Branches elenca i branch, il principale (HEAD del repo bare) per primo e gli
// altri dal più recente. Un repo vuoto torna l'elenco vuoto.
func (s *Service) Branches(ctx context.Context, repoID string) (*BranchList, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	out, err := s.run.Output(ctx, dir, nil, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:lstrip=2)%1f%(objectname)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	def := ""
	if o, err := s.run.Output(ctx, dir, nil, "symbolic-ref", "-q", "HEAD"); err == nil {
		def = strings.TrimPrefix(strings.TrimSpace(string(o)), "refs/heads/")
	} else {
		var ge *gitrun.Error
		if !errors.As(err, &ge) || ge.ExitCode != 1 { // 1 = HEAD staccato
			return nil, err
		}
	}
	type row struct{ name, sha string }
	var rows []row
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		n, sha, ok := strings.Cut(line, "\x1f")
		if !ok {
			return nil, fmt.Errorf("gitread: riga di for-each-ref inattesa: %q", line)
		}
		r := row{n, sha}
		if n == def {
			rows = append([]row{r}, rows...)
		} else {
			rows = append(rows, r)
		}
		shas = append(shas, sha)
	}
	m, err := s.summaries(ctx, dir, shas)
	if err != nil {
		return nil, err
	}
	res := &BranchList{Items: []Branch{}, Total: len(rows)}
	for _, r := range rows {
		res.Items = append(res.Items, Branch{Name: r.name, IsDefault: r.name == def, Commit: m[r.sha]})
	}
	return res, nil
}

// Tag è un tag (Tag del contratto, senza gli indirizzi che aggiunge core).
type Tag struct {
	Name      string    `json:"name"`
	Annotated bool      `json:"annotated"`
	Message   string    `json:"message,omitempty"`
	TaggedAt  time.Time `json:"taggedAt"`
	Commit    Commit    `json:"commit"`
}

// TagList è l'elenco dei tag.
type TagList struct {
	Items []Tag `json:"items"`
	Total int   `json:"total"`
}

const tagFormat = "%(refname:lstrip=2)%1f%(objecttype)%1f%(objectname)%1f%(*objecttype)%1f%(*objectname)%1f%(creatordate:iso-strict)%1f%(contents)%1e"

// Tags elenca i tag dal più recente. Per un tag annotato data e messaggio
// sono quelli del tag, per uno leggero la data è quella del commit (B7). I
// tag che non puntano a un commit si saltano.
func (s *Service) Tags(ctx context.Context, repoID string) (*TagList, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	out, err := s.run.Output(ctx, dir, nil, "for-each-ref", "--sort=-creatordate", "--format="+tagFormat, "refs/tags/")
	if err != nil {
		return nil, err
	}
	var tags []Tag
	var shas []string
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 7)
		if len(f) != 7 {
			return nil, fmt.Errorf("gitread: record di tag inatteso (%d campi)", len(f))
		}
		t := Tag{Name: f[0]}
		var commit string
		switch {
		case f[1] == "commit":
			commit = f[2]
		case f[1] == "tag" && f[3] == "commit":
			commit = f[4]
			t.Annotated = true
			t.Message = strings.TrimRight(f[6], "\n")
		default:
			continue
		}
		at, err := time.Parse(time.RFC3339, f[5])
		if err != nil {
			return nil, fmt.Errorf("gitread: data del tag %q: %w", f[0], err)
		}
		t.TaggedAt = at
		tags = append(tags, t)
		shas = append(shas, commit)
	}
	m, err := s.summaries(ctx, dir, shas)
	if err != nil {
		return nil, err
	}
	for i := range tags {
		tags[i].Commit = m[shas[i]]
	}
	if tags == nil {
		tags = []Tag{}
	}
	return &TagList{Items: tags, Total: len(tags)}, nil
}

// Archive è un archivio pronto da mandare in streaming.
type Archive struct {
	SHA         string
	Filename    string
	ContentType string

	format string
	prefix string
	svc    *Service
	dir    string
}

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,99}$`)

var slugRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// PrepareArchive risolve ref (branch, tag o commit) e prepara l'archivio
// "zip" o "tar.gz" dell'albero. name è il nome del repo per il nome del file
// e per la cartella radice dentro l'archivio (`<name>-<ref>/`). ErrInvalidInput
// per un formato o un nome non validi.
func (s *Service) PrepareArchive(ctx context.Context, repoID, ref, format, name string) (*Archive, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	a := &Archive{svc: s, dir: dir, format: format}
	switch format {
	case "", "zip":
		a.format, a.ContentType = "zip", "application/zip"
	case "tar.gz":
		a.ContentType = "application/gzip"
	default:
		return nil, fmt.Errorf("%w: format", ErrInvalidInput)
	}
	if !repoNameRE.MatchString(name) || strings.HasSuffix(strings.ToLower(name), ".git") {
		return nil, fmt.Errorf("%w: name", ErrInvalidInput)
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		return nil, err
	}
	slug := strings.Trim(slugRE.ReplaceAllString(ref, "-"), "-.")
	if gitref.IsHexSHA(ref) && len(slug) > 12 {
		slug = slug[:12]
	}
	if slug == "" {
		slug = sha[:12]
	}
	a.SHA = sha
	a.prefix = name + "-" + slug
	a.Filename = a.prefix + "." + a.format
	return a, nil
}

// WriteTo manda l'archivio a w con `git archive`, in streaming.
func (a *Archive) WriteTo(ctx context.Context, w io.Writer) error {
	return a.svc.run.Stream(ctx, a.dir, w, "archive", "--format="+a.format, "--prefix="+a.prefix+"/", a.SHA)
}
