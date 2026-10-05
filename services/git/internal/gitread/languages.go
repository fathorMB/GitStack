package gitread

import (
	"context"
	"errors"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/languages"
)

// Languages torna i byte per lingua di ref (branch, tag o sha), con le
// percentuali (Languages del contratto). Il risultato è in cache per sha del
// commit. Un repo vuoto (nessun branch né tag) torna lista vuota e 0 byte; un
// ref inesistente in un repo non vuoto è gitref.ErrRefNotFound.
func (s *Service) Languages(ctx context.Context, repoID, ref string) (*languages.Result, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, ref)
	if err != nil {
		if errors.Is(err, gitref.ErrRefNotFound) {
			out, rerr := s.run.Output(ctx, dir, nil, "for-each-ref", "--count=1", "--format=%(refname)")
			if rerr != nil {
				return nil, rerr
			}
			if len(out) == 0 {
				return &languages.Result{Languages: []languages.Share{}}, nil
			}
		}
		return nil, err
	}
	if r, ok := s.langs.Get(sha); ok {
		return &r, nil
	}
	items, err := s.lsTree(ctx, dir, sha, "", true)
	if err != nil {
		return nil, err
	}
	files := make([]languages.File, 0, len(items))
	for _, it := range items {
		files = append(files, languages.File{Path: it.Path, Mode: it.Mode, Type: it.Type, Size: it.Size})
	}
	r := languages.Compute(files)
	s.langs.Put(sha, r)
	return &r, nil
}
