package gitread

import (
	"context"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

// ResolveCommit risolve uno sha (completo o prefisso di almeno 7 cifre) nello
// sha completo di un commit del repo: serve a core per agganciare gli stati
// dei commit (PR9) senza leggere il diff. gitref.ErrInvalidSHA se non è
// esadecimale, gitref.ErrRefNotFound se non esiste, è ambiguo o non è un commit.
func (s *Service) ResolveCommit(ctx context.Context, repoID, sha string) (string, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return "", err
	}
	return gitref.ResolveCommit(ctx, s.run, dir, sha)
}
