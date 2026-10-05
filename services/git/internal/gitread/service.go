package gitread

import (
	"errors"
	"fmt"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/languages"
)

// Service legge la storia dei repo. Dir risolve l'id del repo nel percorso
// del repo bare e torna un errore (che resta nella catena, es. ErrNotFound di
// repostore) se non esiste.
type Service struct {
	run *gitrun.Runner
	dir func(repoID string) (string, error)

	langs *languages.Cache // lingue per sha del commit

	searchTimeout time.Duration // 0 = SearchTimeout
}

// New crea il servizio.
func New(run *gitrun.Runner, dir func(repoID string) (string, error)) *Service {
	return &Service{run: run, dir: dir, langs: languages.NewCache()}
}

// ErrRepo avvolge l'errore di risoluzione del repo.
var ErrRepo = errors.New("gitread: repo")

func (s *Service) repoDir(repoID string) (string, error) {
	d, err := s.dir(repoID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRepo, err)
	}
	return d, nil
}
