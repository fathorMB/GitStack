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

// CompareMaxCommits è il massimo di commit elencati da un confronto; oltre,
// CommitsTruncated è true (i conteggi AheadBy e BehindBy restano esatti).
const CompareMaxCommits = 250

// Comparison è il confronto base...head (tre punti): i commit di head che
// base non ha e il diff dal merge-base a head, con i limiti di B6.
type Comparison struct {
	Base string `json:"base"`
	Head string `json:"head"`
	// BaseSHA e HeadSHA sono i commit a cui i due ref puntano ora.
	BaseSHA string `json:"baseSha"`
	HeadSHA string `json:"headSha"`
	// MergeBase è vuoto se le due storie non hanno antenati in comune.
	MergeBase string `json:"mergeBase"`
	// AheadBy: commit di head non in base; BehindBy: commit di base non in head.
	AheadBy  int `json:"aheadBy"`
	BehindBy int `json:"behindBy"`
	// Commits: quelli di head non in base, dal più recente.
	Commits          []Commit   `json:"commits"`
	CommitsTruncated bool       `json:"commitsTruncated"`
	Files            []FileDiff `json:"files"`
	FilesChanged     int        `json:"filesChanged"`
	Additions        int        `json:"additions"`
	Deletions        int        `json:"deletions"`
	Truncated        bool       `json:"truncated"`
	ListOnly         bool       `json:"listOnly"`
}

// Compare confronta due ref (branch, tag o sha) dello stesso repo.
func (s *Service) Compare(ctx context.Context, repoID, base, head string, ignoreWhitespace bool) (*Comparison, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	baseSHA, err := gitref.Resolve(ctx, s.run, dir, base)
	if err != nil {
		return nil, err
	}
	headSHA, err := gitref.Resolve(ctx, s.run, dir, head)
	if err != nil {
		return nil, err
	}
	c := &Comparison{Base: base, Head: head, BaseSHA: baseSHA, HeadSHA: headSHA, Commits: []Commit{}}

	mb, err := s.run.Output(ctx, dir, nil, "merge-base", baseSHA, headSHA)
	switch {
	case err == nil:
		c.MergeBase = strings.TrimSpace(string(mb))
	default:
		var ge *gitrun.Error
		if !errors.As(err, &ge) || ge.ExitCode != 1 {
			return nil, err
		}
	}

	counts, err := s.run.Output(ctx, dir, nil, "rev-list", "--left-right", "--count", baseSHA+"..."+headSHA)
	if err != nil {
		return nil, err
	}
	f := strings.Fields(string(counts))
	if len(f) != 2 {
		return nil, fmt.Errorf("gitread: rev-list --count inatteso %q", counts)
	}
	if c.BehindBy, err = strconv.Atoi(f[0]); err != nil {
		return nil, fmt.Errorf("gitread: rev-list --count: %w", err)
	}
	if c.AheadBy, err = strconv.Atoi(f[1]); err != nil {
		return nil, fmt.Errorf("gitread: rev-list --count: %w", err)
	}

	out, err := s.run.Output(ctx, dir, nil, "log", "--no-color", "--format="+commitFormat,
		"--max-count="+strconv.Itoa(CompareMaxCommits+1), headSHA, "^"+baseSHA, "--")
	if err != nil {
		return nil, err
	}
	commits, err := parseCommits(out)
	if err != nil {
		return nil, err
	}
	if len(commits) > CompareMaxCommits {
		commits, c.CommitsTruncated = commits[:CompareMaxCommits], true
	}
	if commits != nil {
		c.Commits = commits
	}

	from := c.MergeBase
	if from == "" {
		empty, err := s.run.Output(ctx, dir, bytes.NewReader(nil), "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return nil, err
		}
		from = strings.TrimSpace(string(empty))
	}
	rd, err := s.diffRange(ctx, dir, from, headSHA, ignoreWhitespace)
	if err != nil {
		return nil, err
	}
	c.Files, c.FilesChanged, c.Additions, c.Deletions = rd.Files, rd.FilesChanged, rd.Additions, rd.Deletions
	c.Truncated, c.ListOnly = rd.Truncated, rd.ListOnly
	if c.Files == nil {
		c.Files = []FileDiff{}
	}
	return c, nil
}
