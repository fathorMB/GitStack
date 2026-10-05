package gitread

import (
	"errors"
	"fmt"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Service legge la storia dei repo. Dir risolve l'id del repo nel percorso
// del repo bare e torna un errore (che resta nella catena, es. ErrNotFound di
// repostore) se non esiste.
type Service struct {
	run *gitrun.Runner
	dir func(repoID string) (string, error)
}

// New crea il servizio.
func New(run *gitrun.Runner, dir func(repoID string) (string, error)) *Service {
	return &Service{run: run, dir: dir}
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
